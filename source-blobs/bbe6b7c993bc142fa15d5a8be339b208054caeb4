"""Actual offline PEFT merge, independent standalone reload and paired generation."""
from dataclasses import asdict
from functools import wraps
import gc
import hashlib
import importlib.metadata
import os
from pathlib import Path
import re
import shutil
import time
from . import VERSION
from .configuration import CONTRACT, LABELS, PreparationError, digest, instruction_prompt, compact
from .evaluation import prediction_label, score, gate
from .inputs import hash_file, heldout_rows, read_json, regular, tensor_envelope, tensor_digest, verify_adapter, verify_inventory


def private_writes(fn):
    @wraps(fn)
    def call(*args, **kwargs):
        previous = os.umask(0o077)
        try:
            return fn(*args, **kwargs)
        finally:
            os.umask(previous)
    return call


def check_cancelled(cancelled):
    if cancelled():
        raise PreparationError('MODEL_PREPARATION_CANCELLED')


def target_weight(name):
    return re.fullmatch(r'model\.layers\.[0-9]+\.self_attn\.(q_proj|v_proj)\.weight', name) is not None


def standalone_shapes(original_shapes, original_digests, model_configuration):
    aliases = CONTRACT['serialized_tied_weight_aliases']
    if model_configuration.get('tie_word_embeddings') is not True:
        raise PreparationError('MODEL_PREPARATION_MODEL_INVALID')
    for alias, source in aliases.items():
        if alias not in original_shapes or source not in original_shapes or original_shapes[alias] != original_shapes[source] or original_digests[alias] != original_digests[source]:
            raise PreparationError('MODEL_PREPARATION_MODEL_INVALID')
    return {name: shape for name, shape in original_shapes.items() if name not in aliases}


def check_tensors(path, dtype, expected_shapes=None):
    import torch
    from safetensors import safe_open
    envelope = tensor_envelope(path, dtype)
    shapes = {name: item['shape'] for name, item in envelope.items()}
    if expected_shapes is not None and shapes != expected_shapes:
        raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
    with safe_open(path, framework='pt', device='cpu') as file:
        for name in file.keys():
            tensor = file.get_tensor(name)
            if not torch.isfinite(tensor).all().item():
                raise PreparationError('MODEL_PREPARATION_NONFINITE_TENSOR')
            del tensor
    return envelope


def encode_rows(tokenizer, rows, configuration):
    encoded = []
    special = tuple(value for value in tokenizer.all_special_tokens if value)
    for row in rows:
        if any(value in row.prompt for value in special):
            raise PreparationError('MODEL_PREPARATION_RESERVED_TOKEN', row.line_number)
        tokens = tokenizer.apply_chat_template([{'role': 'user', 'content': instruction_prompt(row.prompt)}],
                  tokenize=True, add_generation_prompt=True, enable_thinking=False, return_dict=False)
        if not isinstance(tokens, list) or not tokens or any(type(v) is not int or v < 0 for v in tokens):
            raise PreparationError('MODEL_PREPARATION_TOKENIZER_INVALID')
        if len(tokens) > configuration.maximum_input_tokens or len(tokens) + configuration.maximum_new_tokens > CONTRACT['maximum_context_tokens']:
            raise PreparationError('MODEL_PREPARATION_SEQUENCE_TOO_LONG', row.line_number)
        encoded.append(tokens)
    return encoded


def generate_rows(model, tokenizer, encoded, configuration, cancelled, emit, phase):
    import torch
    model.eval()
    observations = []
    for position, tokens in enumerate(encoded):
        check_cancelled(cancelled)
        started = time.monotonic_ns()
        inputs = torch.tensor([tokens], dtype=torch.long, device=model.device)
        with torch.inference_mode():
            generated = model.generate(input_ids=inputs, attention_mask=torch.ones_like(inputs),
                do_sample=False, num_beams=1, use_cache=True, max_new_tokens=configuration.maximum_new_tokens,
                eos_token_id=tokenizer.eos_token_id, pad_token_id=tokenizer.pad_token_id)
        continuation = generated[0, len(tokens):].detach().cpu().tolist()
        if not 0 < len(continuation) <= configuration.maximum_new_tokens:
            raise PreparationError('MODEL_PREPARATION_OUTPUT_INVALID')
        raw = tokenizer.decode(continuation, skip_special_tokens=True)
        observation = {'predicted_label': prediction_label(raw), 'output_sha256': hashlib.sha256(raw.encode('utf-8')).hexdigest(),
                       'generated_tokens': len(continuation), 'duration_milliseconds': (time.monotonic_ns() - started) // 1000000}
        observations.append(observation)
        emit({'kind': 'evaluation_progress', 'phase': phase, 'completed_rows': position + 1, 'total_rows': len(encoded)})
        del inputs, generated
    return observations


@private_writes
def prepare(model_directory, catalog, adapter_directory, adapter_files, training_configuration,
            heldout_file, heldout_sha256, output_directory, configuration, data_limits, maximum_output_bytes, maximum_output_files, cancelled, emit):
    import torch
    from transformers import AutoModelForCausalLM, AutoTokenizer, set_seed
    from peft import PeftModel
    from projectone_training.configuration import runtime_contract
    from projectone_training.catalog import catalog_digest

    check_cancelled(cancelled)
    if type(maximum_output_bytes) is not int or not 1 <= maximum_output_bytes <= CONTRACT['bounds']['maximum_output_bytes'] or type(maximum_output_files) is not int or not 1 <= maximum_output_files <= CONTRACT['bounds']['maximum_output_files']:
        raise PreparationError('MODEL_PREPARATION_RESOURCES_INVALID')
    runtime = runtime_contract()
    actual = {name: importlib.metadata.version(name) for name in runtime['packages']}
    if actual != runtime['packages']:
        raise PreparationError('MODEL_PREPARATION_RUNTIME_MISMATCH')
    output = Path(output_directory)
    if output.resolve(strict=True) != output or not output.is_dir() or output.stat().st_uid != os.geteuid() or output.stat().st_mode & 0o077 or list(output.iterdir()):
        raise PreparationError('MODEL_PREPARATION_OUTPUT_CONFLICT')
    expected_catalog = read_json(Path(__file__).resolve().parents[3] / 'contracts/agent/v1/model-catalog.json', 1048576)
    if compact(catalog) != compact(expected_catalog):
        raise PreparationError('MODEL_PREPARATION_MODEL_INVALID')
    verify_inventory(model_directory, catalog['files'])
    model_config = read_json(model_directory / 'config.json', 65536)
    verify_adapter(adapter_directory, adapter_files, training_configuration, model_config)
    check_tensors(adapter_directory / 'adapter_model.safetensors', 'F32')
    original_weights = model_directory / 'model.safetensors'
    original = tensor_envelope(original_weights, 'BF16')
    original_shapes = {name: value['shape'] for name, value in original.items()}
    before = {name: tensor_digest(original_weights, item) for name, item in original.items()}
    estimated_output = sum(asset['size_bytes'] for asset in catalog['files']) - sum(original[name]['bytes'] for name in CONTRACT['serialized_tied_weight_aliases']) + CONTRACT['bounds']['maximum_tensor_header_bytes']
    if estimated_output > maximum_output_bytes or len(catalog['files']) > maximum_output_files:
        raise PreparationError('MODEL_PREPARATION_OUTPUT_LIMIT')
    rows = heldout_rows(heldout_file, heldout_sha256, data_limits)
    cpu = configuration.device == 'cpu_verification'
    required_device = CONTRACT['production_device']
    if not cpu and (not torch.cuda.is_available() or torch.cuda.device_count() != required_device['device_count'] or torch.cuda.get_device_capability(0)[0] < required_device['minimum_compute_major'] or not torch.cuda.is_bf16_supported()):
        raise PreparationError('MODEL_PREPARATION_DEVICE_UNAVAILABLE')
    torch.set_num_threads(configuration.threads)
    if torch.get_num_interop_threads() != 1:
        torch.set_num_interop_threads(1)
    set_seed(configuration.seed)
    tokenizer = AutoTokenizer.from_pretrained(model_directory, local_files_only=True, trust_remote_code=False)
    if not isinstance(tokenizer.chat_template, str):
        raise PreparationError('MODEL_PREPARATION_TOKENIZER_INVALID')
    template_sha256 = hashlib.sha256(tokenizer.chat_template.encode('utf-8')).hexdigest()
    encoded = encode_rows(tokenizer, rows, configuration)
    if type(tokenizer.eos_token_id) is not int or type(tokenizer.pad_token_id) is not int:
        raise PreparationError('MODEL_PREPARATION_TOKENIZER_INVALID')
    dtype = torch.float32 if cpu else torch.bfloat16
    device = 'cpu' if cpu else 'cuda:0'

    def load(directory):
        return AutoModelForCausalLM.from_pretrained(directory, dtype=dtype, local_files_only=True,
                 trust_remote_code=False, use_safetensors=True, attn_implementation='eager').to(device)

    emit({'kind': 'phase', 'phase': 'evaluating_base'})
    base = load(model_directory)
    baseline = generate_rows(base, tokenizer, encoded, configuration, cancelled, emit, 'base')
    del base
    gc.collect()
    if not cpu:
        torch.cuda.empty_cache()
    check_cancelled(cancelled)
    emit({'kind': 'phase', 'phase': 'merging'})
    base = load(model_directory)
    adapted = PeftModel.from_pretrained(base, adapter_directory, local_files_only=True, is_trainable=False)
    try:
        merged = adapted.merge_and_unload(safe_merge=True)
    except ValueError:
        raise PreparationError('MODEL_PREPARATION_MERGE_INVALID') from None
    if any('lora_' in name for name in merged.state_dict()):
        raise PreparationError('MODEL_PREPARATION_MERGE_INVALID')
    merged.to(dtype=torch.bfloat16)
    model_output = output / 'model'
    model_output.mkdir(mode=0o700)
    merged.save_pretrained(model_output, safe_serialization=True, max_shard_size='2GB')
    del adapted, merged, base
    gc.collect()
    if not cpu:
        torch.cuda.empty_cache()
    # Preserve the exact approved architecture/tokenizer/license/config bytes after supported serialization.
    for asset in catalog['files']:
        if asset['path'] != 'model.safetensors':
            shutil.copyfile(model_directory / asset['path'], model_output / asset['path'])
            os.chmod(model_output / asset['path'], 0o600)
    expected_names = {item['path'] for item in catalog['files']}
    if {p.name for p in model_output.iterdir()} != expected_names:
        raise PreparationError('MODEL_PREPARATION_OUTPUT_INVALID')
    candidate_weights = model_output / 'model.safetensors'
    candidate = check_tensors(candidate_weights, 'BF16', standalone_shapes(original_shapes, before, model_config))
    changed_targets = []
    for name, item in candidate.items():
        changed = before[name] != tensor_digest(candidate_weights, item)
        if changed and not target_weight(name):
            raise PreparationError('MODEL_PREPARATION_NON_TARGET_CHANGED')
        if changed:
            changed_targets.append(name)
    if not changed_targets:
        raise PreparationError('MODEL_PREPARATION_UNCHANGED_MODEL')
    check_cancelled(cancelled)
    emit({'kind': 'phase', 'phase': 'evaluating_candidate'})
    candidate_model = load(model_output)
    predictions = generate_rows(candidate_model, tokenizer, encoded, configuration, cancelled, emit, 'candidate')
    del candidate_model
    gc.collect()
    if not cpu:
        torch.cuda.empty_cache()
    verify_inventory(model_directory, catalog['files'])
    verify_inventory(adapter_directory, adapter_files)
    if hash_file(heldout_file) != heldout_sha256:
        raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
    files = []
    for path in sorted(model_output.iterdir()):
        info = regular(path)
        if info.st_mode & 0o077:
            raise PreparationError('MODEL_PREPARATION_OUTPUT_INVALID')
        with path.open('rb') as stream:
            os.fsync(stream.fileno())
        files.append({'path': path.name, 'size_bytes': info.st_size, 'sha256': hash_file(path)})
    if len(files) > maximum_output_files or sum(v['size_bytes'] for v in files) > maximum_output_bytes:
        raise PreparationError('MODEL_PREPARATION_OUTPUT_LIMIT')
    descriptor = os.open(model_output, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    expected = [row.completion for row in rows]
    base_score = score(expected, [v['predicted_label'] for v in baseline])
    candidate_score = score(expected, [v['predicted_label'] for v in predictions])
    check_cancelled(cancelled)
    return {'schema_version': 1, 'worker_version': VERSION, 'recipe_id': CONTRACT['recipe_id'], 'recipe_sha256': digest(CONTRACT),
            'configuration': asdict(configuration), 'configuration_sha256': configuration.digest(),
            'model_catalog_sha256': catalog_digest(catalog), 'adapter_files': adapter_files, 'heldout_sha256': heldout_sha256,
            'training_configuration_sha256': digest(training_configuration), 'chat_template_sha256': template_sha256,
            'instruction_sha256': hashlib.sha256(CONTRACT['instruction'].encode('utf-8')).hexdigest(),
            'packages': actual, 'device_profile': configuration.device, 'computation_dtype': 'float32' if cpu else 'bfloat16',
            'storage_dtype': CONTRACT['storage_dtype'], 'row_count': len(rows), 'base': base_score, 'candidate': candidate_score,
            'cases': [{'source_id': row.source_id, 'prompt_sha256': hashlib.sha256(row.prompt.encode('utf-8')).hexdigest(),
                       'expected_label': row.completion, 'base': a, 'candidate': b} for row, a, b in zip(rows, baseline, predictions)],
            'model_files': files, 'changed_target_tensors': sorted(changed_targets),
            'tied_weight_aliases': CONTRACT['serialized_tied_weight_aliases'],
            'gate_passed': gate(base_score['correct'], candidate_score['correct'], len(rows), configuration.minimum_accuracy_basis_points, configuration.maximum_regression_basis_points)}
