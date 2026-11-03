"""Private immutable Trainer checkpoint receipts and hash-bound compatibility checks."""
from dataclasses import asdict, dataclass
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import uuid
from projectone_data.validation import unique, invalid_constant, DataError
from .configuration_generated import CONTRACT
from .data import TrainingError
from .processor_generated import PROCESSOR_CONTRACT

def file_digest(path):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1048576), b''):
            digest.update(block)
    return digest.hexdigest()


def inventory(directory, maximum_bytes, maximum_files):
    result = []
    total = 0
    for path in sorted(directory.rglob('*')):
        info = path.lstat()
        if stat.S_ISDIR(info.st_mode):
            continue
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise TrainingError('TRAINING_OUTPUT_INVALID')
        total += info.st_size
        if total > maximum_bytes or len(result) >= maximum_files:
            raise TrainingError('TRAINING_OUTPUT_LIMIT')
        result.append({'path': str(path.relative_to(directory)), 'size_bytes': info.st_size, 'sha256': file_digest(path)})
    return result


def publish_private_json(directory, name, value, maximum):
    raw = json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
    if len(raw) > maximum:
        raise TrainingError('TRAINING_RESULT_LIMIT')
    temporary = directory / ('.pending-' + uuid.uuid4().hex + '.json')
    with temporary.open('xb') as stream:
        os.chmod(stream.fileno(), 0o600)
        stream.write(raw)
        stream.flush()
        os.fsync(stream.fileno())
    os.link(temporary, directory / name)
    temporary.unlink()
    descriptor = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def seal_checkpoint(directory, bindings, step, maximum_bytes, maximum_files):
    directory.chmod(0o700)
    receipt = directory / 'projectone-checkpoint.json'
    if receipt.exists():
        raise TrainingError('TRAINING_CHECKPOINT_CONFLICT')
    files = inventory(directory, maximum_bytes, maximum_files)
    names = {item['path'] for item in files}
    if not {'adapter_model.safetensors', 'adapter_config.json', 'optimizer.pt', 'scheduler.pt', 'trainer_state.json', 'rng_state.pth'}.issubset(names):
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPLETE')
    trainer_state = json.loads((directory / 'trainer_state.json').read_text())
    if trainer_state['global_step'] != step:
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPLETE')
    for asset in files:
        with (directory / asset['path']).open('rb') as stream:
            os.fsync(stream.fileno())
    value = {'schema_version': 1, 'bindings': bindings, 'global_step': step, 'files': files}
    publish_private_json(directory, receipt.name, value, CONTRACT['wire_limits']['maximum_checkpoint_metadata_bytes'])
    return {'global_step': step, 'receipt_sha256': file_digest(receipt), 'path': directory.name}


@dataclass(frozen=True)
class CheckpointReference:
    directory: Path
    receipt_sha256: str


def metadata(path):
    ceiling = CONTRACT['wire_limits']['maximum_checkpoint_metadata_bytes']
    if not path.is_file() or path.is_symlink() or path.stat().st_size > ceiling:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    try:
        return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=unique, parse_constant=invalid_constant)
    except (ValueError, UnicodeError, DataError, RecursionError):
        raise TrainingError('TRAINING_CHECKPOINT_INVALID') from None


def verify_checkpoint(reference, bindings, configuration, maximum_bytes, maximum_files):
    try:
        return verified_checkpoint(reference, bindings, configuration, maximum_bytes, maximum_files)
    except (OSError, ValueError, TypeError, KeyError, RecursionError):
        raise TrainingError('TRAINING_CHECKPOINT_INVALID') from None


def verified_checkpoint(reference, bindings, configuration, maximum_bytes, maximum_files):
    directory = reference.directory
    if not re.fullmatch('[a-f0-9]{64}', reference.receipt_sha256) or directory.resolve(strict=True) != directory:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    info = directory.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    receipt = directory / 'projectone-checkpoint.json'
    info = receipt.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_size > CONTRACT['wire_limits']['maximum_checkpoint_metadata_bytes']:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    if file_digest(receipt) != reference.receipt_sha256:
        raise TrainingError('TRAINING_CHECKPOINT_CHANGED')
    value = metadata(receipt)
    if not isinstance(value, dict) or set(value) != {'schema_version', 'bindings', 'global_step', 'files'} or type(value['schema_version']) is not int or value['schema_version'] != 1:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    canonical = lambda item: json.dumps(item, sort_keys=True, separators=(',', ':'), allow_nan=False)
    if bindings.get('configuration_sha256') != configuration.digest() or canonical(bindings.get('configuration')) != canonical(asdict(configuration)):
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPATIBLE')
    if canonical(value['bindings']) != canonical(bindings):
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPATIBLE')
    step = value['global_step']
    if type(step) is not int or not 1 <= step <= configuration.maximum_steps:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    required = {'adapter_model.safetensors', 'adapter_config.json', 'optimizer.pt', 'scheduler.pt', 'trainer_state.json', 'rng_state.pth'}
    allowed = required | {'README.md', 'training_args.bin'}
    files = value['files']
    if not isinstance(files, list) or not 1 <= len(files) <= min(maximum_files, CONTRACT['wire_limits']['maximum_checkpoint_files']):
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    names = set()
    total = 0
    for asset in files:
        if not isinstance(asset, dict) or set(asset) != {'path', 'size_bytes', 'sha256'}:
            raise TrainingError('TRAINING_CHECKPOINT_INVALID')
        name = asset['path']
        if not isinstance(name, str) or name not in allowed or name in names or type(asset['size_bytes']) is not int or asset['size_bytes'] < 0 or not isinstance(asset['sha256'], str) or not re.fullmatch('[a-f0-9]{64}', asset['sha256']):
            raise TrainingError('TRAINING_CHECKPOINT_INVALID')
        names.add(name)
        total += asset['size_bytes']
        if total > min(maximum_bytes, CONTRACT['wire_limits']['maximum_checkpoint_bytes']):
            raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    if not required.issubset(names):
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPLETE')
    observed_names = set()
    with os.scandir(directory) as entries:
        for entry in entries:
            if len(observed_names) >= maximum_files + 1 or not entry.is_file(follow_symlinks=False):
                raise TrainingError('TRAINING_CHECKPOINT_INVALID')
            observed_names.add(entry.name)
    if observed_names != names | {receipt.name}:
        raise TrainingError('TRAINING_CHECKPOINT_CHANGED')
    actual = inventory(directory, maximum_bytes + receipt.stat().st_size, maximum_files + 1)
    actual = [item for item in actual if item['path'] != receipt.name]
    if canonical(sorted(files, key=lambda item: item['path'])) != canonical(actual):
        raise TrainingError('TRAINING_CHECKPOINT_CHANGED')
    if 'README.md' in names and (directory / 'README.md').read_bytes() != PROCESSOR_CONTRACT['adapter_readme_template'].replace('{repository}', bindings['model_repository']).encode('utf-8'):
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    trainer = metadata(directory / 'trainer_state.json')
    if not isinstance(trainer, dict) or type(trainer.get('global_step')) is not int or trainer['global_step'] != step:
        raise TrainingError('TRAINING_CHECKPOINT_INVALID')
    adapter = metadata(directory / 'adapter_config.json')
    if (not isinstance(adapter, dict) or adapter.get('peft_type') != 'LORA' or adapter.get('task_type') != 'CAUSAL_LM'
            or adapter.get('auto_mapping') is not None or adapter.get('modules_to_save') is not None
            or adapter.get('base_model_name_or_path') != bindings['model_repository']
            or sorted(adapter.get('target_modules', [])) != sorted(CONTRACT['recipe']['modules'])
            or adapter.get('r') != configuration.rank or adapter.get('lora_alpha') != configuration.alpha
            or adapter.get('lora_dropout') != float(configuration.dropout) or adapter.get('bias') != 'none'
            or adapter.get('target_parameters') is not None or adapter.get('layer_replication') is not None
            or adapter.get('use_dora') or adapter.get('use_rslora')):
        raise TrainingError('TRAINING_CHECKPOINT_INCOMPATIBLE')
    return value, trainer

