"""Real offline Trainer/PEFT LoRA computation. This layer never commits a business job or publishes a model."""
from dataclasses import asdict
import importlib.metadata
import math
import os
from pathlib import Path
import stat
import uuid
from .configuration import Configuration, runtime_contract
from .configuration_generated import CONTRACT
from .data import CompletionCollator, TrainingError, load_training_tokenizer, tokenize_file
from .checkpoints import file_digest, inventory, seal_checkpoint, sync_directory, verify_checkpoint
from .catalog import catalog_digest
from .processor_generated import PROCESSOR_CONTRACT


def write_adapter_readme(directory, repository):
    # Replace only this run's PEFT-generated card before sealing or exposing any output inventory.
    with (directory / 'README.md').open('wb') as stream:
        os.chmod(stream.fileno(), 0o600)
        stream.write(PROCESSOR_CONTRACT['adapter_readme_template'].replace('{repository}', repository).encode('utf-8'))
        stream.flush()
        os.fsync(stream.fileno())




def verify_model(directory, catalog):
    if directory.resolve(strict=True) != directory or not directory.is_dir():
        raise TrainingError('TRAINING_MODEL_INVALID')
    expected = {item['path'] for item in catalog['files']}
    if set(path.name for path in directory.iterdir()) != expected:
        raise TrainingError('TRAINING_MODEL_INVALID')
    for asset in catalog['files']:
        path = directory / asset['path']
        info = path.lstat()
        if path.name != asset['path'] or not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_size != asset['size_bytes'] or file_digest(path) != asset['sha256']:
            raise TrainingError('TRAINING_MODEL_CHANGED')






def train(model_directory: Path, catalog: dict, train_file: Path, train_sha256: str,
          validation_file: Path, validation_sha256: str, output: Path, configuration: Configuration,
          limits, maximum_output_bytes: int, maximum_output_files: int, cancelled, emit,
          execution_identity: dict, scope_identity: dict, resume_checkpoint=None):
    import torch
    from transformers import AutoModelForCausalLM, Trainer, TrainerCallback, TrainingArguments, set_seed
    from peft import LoraConfig, TaskType, get_peft_model

    recipe = CONTRACT['recipe']
    runtime = runtime_contract()
    actual_packages = {name: importlib.metadata.version(name) for name in runtime['packages']}
    if actual_packages != runtime['packages']:
        raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    if output.resolve(strict=True) != output or output.stat().st_uid != os.geteuid() or output.stat().st_mode & 0o077 or list(output.iterdir()):
        raise TrainingError('TRAINING_OUTPUT_CONFLICT')
    if type(maximum_output_bytes) is not int or maximum_output_bytes < 1 or type(maximum_output_files) is not int or maximum_output_files < 1:
        raise TrainingError('TRAINING_CONFIGURATION_INVALID')
    if configuration.maximum_tokens > catalog['max_context']:
        raise TrainingError('TRAINING_CONFIGURATION_INVALID')
    if not isinstance(execution_identity, dict) or not execution_identity:
        raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    if not isinstance(scope_identity, dict) or set(scope_identity) != {'tenant_id', 'workspace_id', 'job_id'}:
        raise TrainingError('TRAINING_SCOPE_INVALID')
    for value in scope_identity.values():
        try:
            if not isinstance(value, str) or str(uuid.UUID(value)) != value or uuid.UUID(value).int == 0:
                raise ValueError('scope')
        except ValueError:
            raise TrainingError('TRAINING_SCOPE_INVALID') from None
    cpu = configuration.device == 'cpu_verification'
    if not cpu and (not torch.cuda.is_available() or torch.cuda.device_count() != 1
                    or torch.cuda.get_device_capability(0)[0] < 8 or not torch.cuda.is_bf16_supported()):
        raise TrainingError('TRAINING_DEVICE_UNAVAILABLE')
    torch.set_num_threads(configuration.threads)
    # Inter-op threads are a fixed single-process recipe invariant.
    if torch.get_num_interop_threads() != recipe['interop_threads']:
        torch.set_num_interop_threads(recipe['interop_threads'])
    set_seed(configuration.seed)
    verify_model(model_directory, catalog)
    tokenizer = load_training_tokenizer(model_directory)
    train_data = tokenize_file(train_file, train_sha256, tokenizer, limits, configuration.maximum_tokens, 'train')
    validation_data = tokenize_file(validation_file, validation_sha256, tokenizer, limits, configuration.maximum_tokens, 'validation')
    if {row['content_sha256'] for row in train_data.provenance} & {row['content_sha256'] for row in validation_data.provenance}:
        raise TrainingError('TRAINING_SPLIT_OVERLAP')
    if cancelled():
        return {'status': 'cancelled', 'global_step': 0, 'checkpoints': []}
    bindings = {'configuration_sha256': configuration.digest(),
                'configuration': asdict(configuration), 'execution_identity': execution_identity, 'scope': scope_identity,
                'model_repository': catalog['repository'],
                'model_catalog_sha256': catalog_digest(catalog),
                'train_sha256': train_sha256, 'validation_sha256': validation_sha256,
                'recipe_version': recipe['version'],
                'packages': actual_packages}
    resume_step = 0
    resume_state = None
    if resume_checkpoint is not None:
        verified, resume_state = verify_checkpoint(resume_checkpoint, bindings, configuration,
                                                 maximum_output_bytes, maximum_output_files)
        resume_step = verified['global_step']
    emit({'kind': 'phase', 'phase': 'loading_model', 'global_step': 0})
    model = AutoModelForCausalLM.from_pretrained(model_directory, dtype=torch.float32 if cpu else torch.bfloat16,
                                               local_files_only=True, trust_remote_code=False,
                                               use_safetensors=True, attn_implementation='eager')
    if model.config.model_type != 'qwen3':
        raise TrainingError('TRAINING_MODEL_INVALID')
    model.config.use_cache = False
    model = get_peft_model(model, LoraConfig(task_type=TaskType.CAUSAL_LM, r=configuration.rank,
                          lora_alpha=configuration.alpha, lora_dropout=float(configuration.dropout),
                          target_modules=recipe['modules'], bias='none'))
    model.peft_config['default'].base_model_name_or_path = catalog['repository']
    adapter_before = {name: value.detach().cpu().clone() for name, value in model.named_parameters() if value.requires_grad}
    if not adapter_before or any('lora_' not in name for name in adapter_before):
        raise TrainingError('TRAINING_ADAPTER_INVALID')
    checkpoints = []
    metrics = []

    class Events(TrainerCallback):
        def on_step_end(self, args, state, control, **kwargs):
            if cancelled():
                control.should_training_stop = True
                control.should_save = state.global_step > 0
            return control

        def on_log(self, args, state, control, logs=None, **kwargs):
            values = {key: float(value) for key, value in (logs or {}).items()
                      if key in {'loss', 'learning_rate', 'grad_norm', 'epoch'}}
            if not all(math.isfinite(value) for value in values.values()):
                raise TrainingError('TRAINING_NONFINITE_METRIC')
            if values:
                event = {'kind': 'metric', 'global_step': state.global_step, 'values': values}
                metrics.append(event)
                emit(event)

        def on_save(self, args, state, control, **kwargs):
            directory = Path(args.output_dir) / ('checkpoint-' + str(state.global_step))
            write_adapter_readme(directory, catalog['repository'])
            sealed = seal_checkpoint(directory, bindings, state.global_step, maximum_output_bytes, maximum_output_files)
            inventory(output, maximum_output_bytes, maximum_output_files)
            checkpoints.append(sealed)
            emit({'kind': 'checkpoint', **sealed})

    checkpoint_directory = output / 'checkpoints'
    checkpoint_directory.mkdir(mode=0o700)
    sync_directory(output)
    arguments = TrainingArguments(output_dir=str(checkpoint_directory), max_steps=configuration.maximum_steps,
                per_device_train_batch_size=configuration.batch_size, per_device_eval_batch_size=configuration.batch_size,
                gradient_accumulation_steps=configuration.gradient_accumulation_steps,
                learning_rate=float(configuration.learning_rate), optim=recipe['optimizer'],
                lr_scheduler_type=recipe['scheduler'], weight_decay=recipe['weight_decay'],
                max_grad_norm=recipe['maximum_gradient_norm'], use_cpu=cpu, bf16=not cpu, fp16=False,
                seed=configuration.seed, data_seed=configuration.seed, full_determinism=recipe['deterministic'],
                dataloader_num_workers=recipe['dataloader_workers'], dataloader_pin_memory=False,
                gradient_checkpointing=recipe['gradient_checkpointing'], report_to=[], push_to_hub=False,
                disable_tqdm=True, logging_strategy='steps', logging_steps=1, logging_nan_inf_filter=False,
                save_strategy='steps', save_steps=configuration.checkpoint_steps, save_only_model=False,
                save_total_limit=None, eval_strategy='no', torch_compile=False, label_names=['labels'])
    trainer = Trainer(model=model, args=arguments, train_dataset=train_data, eval_dataset=validation_data,
                      data_collator=CompletionCollator(tokenizer.pad_token_id), callbacks=[Events()])
    emit({'kind': 'phase', 'phase': 'finalizing_checkpoint' if resume_step == configuration.maximum_steps else 'training',
          'global_step': resume_step})
    loss_kind = 'trainer_reported_training_loss'
    if resume_step == configuration.maximum_steps:
        model.load_adapter(resume_checkpoint.directory, adapter_name='default', is_trainable=True)
        trainer.state.global_step = resume_step
        observed_losses = [float(value['loss']) for value in resume_state.get('log_history', [])
                           if isinstance(value, dict) and 'loss' in value]
        if not observed_losses:
            raise TrainingError('TRAINING_CHECKPOINT_INVALID')
        loss = observed_losses[-1]
        loss_kind = 'saved_checkpoint_step_loss'
    else:
        result = trainer.train(resume_from_checkpoint=str(resume_checkpoint.directory) if resume_checkpoint else None)
        loss = float(result.training_loss)
    if not math.isfinite(loss):
        raise TrainingError('TRAINING_NONFINITE_METRIC')
    if cancelled():
        return {'status': 'cancelled', 'global_step': trainer.state.global_step, 'checkpoints': checkpoints,
                'bindings': bindings, 'metrics': metrics}
    changed = any(not torch.equal(before, dict(model.named_parameters())[name].detach().cpu()) for name, before in adapter_before.items())
    if trainer.state.global_step != configuration.maximum_steps or not changed:
        raise TrainingError('TRAINING_INCOMPLETE')
    if not all(torch.isfinite(value).all().item() for value in model.parameters() if value.requires_grad):
        raise TrainingError('TRAINING_NONFINITE_METRIC')
    adapter = output / 'adapter'
    adapter.mkdir(mode=0o700)
    model.save_pretrained(adapter, safe_serialization=True, save_embedding_layers=False)
    write_adapter_readme(adapter, catalog['repository'])
    files = inventory(output, maximum_output_bytes, maximum_output_files)
    return {'status': 'trained', 'global_step': trainer.state.global_step, 'training_loss': loss, 'loss_kind': loss_kind,
            'resume_from_step': resume_step, 'parent_checkpoint_receipt_sha256': resume_checkpoint.receipt_sha256 if resume_checkpoint else None,
            'adapter_changed': changed, 'base_parameters_frozen': all(not value.requires_grad for name, value in model.named_parameters() if 'lora_' not in name),
            'bindings': bindings, 'checkpoints': checkpoints, 'metrics': metrics, 'files': files,
            'train_rows': len(train_data), 'validation_rows': len(validation_data), 'device_profile': configuration.device}
