"""Exact ordinary-file inventories, safe tensor envelopes and frozen held-out rows."""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
from .configuration import CONTRACT, LABELS, REPO, PreparationError, compact, prompt_fingerprint

CHUNK_BYTES = 1024 * 1024


def unique(pairs):
    value = {}
    for key, item in pairs:
        if key in value:
            raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
        value[key] = item
    return value


def read_json(path, maximum):
    info = regular(path)
    if info.st_size > maximum:
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
    try:
        value = json.loads(path.read_bytes().decode('utf-8', errors='strict'), object_pairs_hook=unique,
                           parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite')))
        compact(value)
        return value
    except (UnicodeError, ValueError):
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID') from None


def regular(path):
    path = Path(path)
    try:
        info = path.lstat()
        if path.resolve(strict=True) != path or not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_nlink != 1:
            raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
        return info
    except OSError:
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID') from None


def hash_file(path, expected_size=None):
    before = regular(path)
    total = 0
    digest = hashlib.sha256()
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor, 'rb') as stream:
        opened = os.fstat(stream.fileno())
        if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
        while raw := stream.read(CHUNK_BYTES):
            total += len(raw)
            digest.update(raw)
        after = os.fstat(stream.fileno())
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns) or (expected_size is not None and total != expected_size):
        raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
    return digest.hexdigest()


def verify_inventory(directory, assets):
    directory = Path(directory)
    if directory.resolve(strict=True) != directory or not directory.is_dir() or not isinstance(assets, list) or not assets:
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
    names = set()
    for asset in assets:
        if not isinstance(asset, dict) or set(asset) != {'path', 'size_bytes', 'sha256'} or not isinstance(asset['path'], str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,100}', asset['path']) or asset['path'] in {'.', '..'} or asset['path'] in names or type(asset['size_bytes']) is not int or asset['size_bytes'] < 1 or not isinstance(asset['sha256'], str) or not re.fullmatch(r'[a-f0-9]{64}', asset['sha256']):
            raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
        names.add(asset['path'])
        if hash_file(directory / asset['path'], asset['size_bytes']) != asset['sha256']:
            raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
    if {p.name for p in directory.iterdir()} != names:
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')


def tensor_envelope(path, dtype):
    info = regular(path)
    with path.open('rb') as stream:
        prefix = stream.read(8)
        if len(prefix) != 8:
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
        length = struct.unpack('<Q', prefix)[0]
        if not 2 <= length <= CONTRACT['bounds']['maximum_tensor_header_bytes'] or 8 + length >= info.st_size:
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
        try:
            header = json.loads(stream.read(length), object_pairs_hook=unique)
        except (UnicodeError, ValueError):
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID') from None
    if not isinstance(header, dict):
        raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
    descriptors = {}
    intervals = []
    for name, item in header.items():
        if name == '__metadata__':
            if not isinstance(item, dict) or any(not isinstance(k, str) or not isinstance(v, str) for k, v in item.items()):
                raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
            continue
        if not isinstance(item, dict) or set(item) != {'dtype', 'shape', 'data_offsets'} or item['dtype'] != dtype or not isinstance(item['shape'], list) or not item['shape'] or any(type(v) is not int or v < 1 for v in item['shape']) or len(item['shape']) > 4 or not isinstance(item['data_offsets'], list) or len(item['data_offsets']) != 2 or any(type(v) is not int or v < 0 for v in item['data_offsets']):
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
        count = 1
        for dimension in item['shape']:
            count *= dimension
        start, end = item['data_offsets']
        if end - start != count * (2 if dtype == 'BF16' else 4) or end > info.st_size - 8 - length:
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
        intervals.append((start, end))
        descriptors[name] = {'shape': item['shape'], 'offset': 8 + length + start, 'bytes': end - start}
    cursor = 0
    for start, end in sorted(intervals):
        if start != cursor:
            raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
        cursor = end
    if not descriptors or cursor != info.st_size - 8 - length:
        raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
    return descriptors


def tensor_digest(path, descriptor):
    digest = hashlib.sha256()
    with path.open('rb') as stream:
        stream.seek(descriptor['offset'])
        remaining = descriptor['bytes']
        while remaining:
            raw = stream.read(min(CHUNK_BYTES, remaining))
            if not raw:
                raise PreparationError('MODEL_PREPARATION_TENSOR_INVALID')
            digest.update(raw)
            remaining -= len(raw)
    return digest.hexdigest()


def verify_adapter(directory, assets, training_configuration, model_configuration):
    from projectone_training.configuration import Configuration as TrainingConfiguration
    from projectone_training.data import TrainingError
    try:
        TrainingConfiguration(**training_configuration)
    except (TrainingError, TypeError):
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID') from None
    bounds = CONTRACT['bounds']
    if not isinstance(assets, list) or len(assets) != bounds['maximum_adapter_files'] or any(not isinstance(v, dict) or set(v) != {'path', 'size_bytes', 'sha256'} or type(v['size_bytes']) is not int or not 1 <= v['size_bytes'] <= bounds['maximum_adapter_bytes'] for v in assets) or sum(v['size_bytes'] for v in assets) > bounds['maximum_adapter_bytes'] or {v['path'] for v in assets} != {'adapter_config.json', 'adapter_model.safetensors', 'README.md'}:
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')
    verify_inventory(directory, assets)
    actual = read_json(directory / 'adapter_config.json', bounds['maximum_adapter_config_bytes'])
    expected = json.loads((REPO / 'contracts/training/v1/adapter-template.json').read_text())
    expected.update(r=training_configuration['rank'], lora_alpha=training_configuration['alpha'], lora_dropout=float(training_configuration['dropout']))
    if not isinstance(actual, dict) or actual.get('target_modules') not in [['q_proj', 'v_proj'], ['v_proj', 'q_proj']]:
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')
    actual['target_modules'] = sorted(actual['target_modules'])
    expected['target_modules'] = sorted(expected['target_modules'])
    if compact(actual) != compact(expected):
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')
    envelope = tensor_envelope(directory / 'adapter_model.safetensors', 'F32')
    expected_tensors = {}
    configuration = model_configuration
    rank = training_configuration['rank']
    for layer in range(configuration['num_hidden_layers']):
        for module in ['q_proj', 'v_proj']:
            output = configuration['num_attention_heads' if module == 'q_proj' else 'num_key_value_heads'] * configuration['head_dim']
            prefix = f'base_model.model.model.layers.{layer}.self_attn.{module}'
            expected_tensors[prefix + '.lora_A.weight'] = [rank, configuration['hidden_size']]
            expected_tensors[prefix + '.lora_B.weight'] = [output, rank]
    if {k: v['shape'] for k, v in envelope.items()} != expected_tensors:
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')


def heldout_rows(path, expected_sha256, limits):
    from projectone_data.validation import inspect_file, DataError
    if hash_file(path) != expected_sha256:
        raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
    try:
        inspected = inspect_file(path, limits)
    except DataError as error:
        raise PreparationError('MODEL_PREPARATION_HELDOUT_INVALID', error.line_number) from None
    if not inspected.valid() or not CONTRACT['bounds']['minimum_rows'] <= len(inspected.rows) <= CONTRACT['bounds']['maximum_rows'] or inspected.source_sha256 != expected_sha256:
        raise PreparationError('MODEL_PREPARATION_HELDOUT_INVALID')
    seen = set()
    for row in inspected.rows:
        fingerprint = prompt_fingerprint(row.prompt)
        if row.completion not in LABELS or fingerprint in seen:
            raise PreparationError('MODEL_PREPARATION_HELDOUT_INVALID', row.line_number)
        seen.add(fingerprint)
    if {row.completion for row in inspected.rows} != set(LABELS):
        raise PreparationError('MODEL_PREPARATION_HELDOUT_INVALID')
    return inspected.rows
