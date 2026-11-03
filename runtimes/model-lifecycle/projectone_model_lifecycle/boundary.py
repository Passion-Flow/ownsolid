"""Protected exact attempt, source, input and process-limit boundary."""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
from .configuration import Configuration, PreparationError, REPO, compact, CONTRACT as RECIPE
from .inputs import read_json, hash_file
from projectone_data.boundary import private_directory, private_file, valid_id
from projectone_data.validation import DataError, Limits
from projectone_training.catalog import catalog_digest
from projectone_training.configuration import Configuration as TrainingConfiguration, runtime_contract
from projectone_training.data import TrainingError
from projectone_training.boundary import apply_limits
from projectone_training.checkpoints import publish_private_json

CONTRACT_FILE = REPO / 'contracts/model-lifecycle/v1/processor.json'
CONTRACT = json.loads(CONTRACT_FILE.read_text())
IDENTIFIERS = ('tenant_id', 'workspace_id', 'preparation_id', 'attempt_id', 'training_job_id', 'training_attempt_id', 'heldout_id')


def exact(value, fields):
    if not isinstance(value, dict) or set(value) != set(fields):
        raise PreparationError('MODEL_PREPARATION_REQUEST_INVALID')


def decoded(path, maximum, scope=None):
    file = private_file(str(path), maximum, scope)
    return read_json(file, maximum)


def worker_source_digest():
    files = []
    for directory in ['runtimes/model-lifecycle', 'runtimes/training', 'runtimes/data-processing']:
        files.extend(p for p in (REPO / directory).rglob('*.py') if '__pycache__' not in p.parts)
    for name in ['contracts/model-lifecycle/v1/recipe.json', 'contracts/model-lifecycle/v1/processor.json', 'contracts/model-lifecycle/v1/worker-errors.json',
                 'runtimes/training/runtime.json', 'contracts/training/v1/configuration.json',
                 'contracts/training/v1/processor.json', 'contracts/training/v1/adapter-template.json',
                 'contracts/agent/v1/model-catalog.json']:
        files.append(REPO / name)
    return hashlib.sha256(compact({str(p.relative_to(REPO)): hash_file(p) for p in sorted(files)})).hexdigest()


def read_request(path, verification_cpu):
    try:
        return validated_request(path, verification_cpu)
    except (DataError, TrainingError, OSError, ValueError, TypeError, KeyError, RecursionError):
        raise PreparationError('MODEL_PREPARATION_REQUEST_INVALID') from None


def validated_request(path, verification_cpu):
    runtime_contract()
    request = decoded(path, CONTRACT['maximum_request_bytes'])
    exact(request, CONTRACT['request_fields'])
    if type(request['schema_version']) is not int or request['schema_version'] != CONTRACT['schema_version'] or request['operation'] != CONTRACT['operation']:
        raise PreparationError('MODEL_PREPARATION_REQUEST_INVALID')
    for name in IDENTIFIERS:
        if not valid_id(request[name]):
            raise PreparationError('MODEL_PREPARATION_SCOPE_INVALID')
    for name in ['model_catalog_sha256', 'heldout_sha256']:
        if not isinstance(request[name], str) or not re.fullmatch('[a-f0-9]{64}', request[name]):
            raise PreparationError('MODEL_PREPARATION_REQUEST_INVALID')
    for name in ['scope_directory', 'model_directory', 'adapter_directory', 'heldout_file', 'output_directory']:
        if not isinstance(request[name], str) or len(request[name].encode('utf-8', errors='strict')) > CONTRACT['maximum_path_bytes'] or any(ord(value) < 32 or ord(value) == 127 for value in request[name]):
            raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
    scope = private_directory(request['scope_directory'])
    tail = [request[name] for name in ['tenant_id', 'workspace_id', 'preparation_id', 'attempt_id']]
    if list(scope.parts[-len(tail):]) != tail or private_file(str(path), CONTRACT['maximum_request_bytes'], scope) != scope / CONTRACT['request_name']:
        raise PreparationError('MODEL_PREPARATION_SCOPE_INVALID')
    marker = decoded(scope / CONTRACT['marker_name'], CONTRACT['maximum_marker_bytes'], scope)
    if compact(marker) != compact({name: request[name] for name in ('schema_version', *IDENTIFIERS)}):
        raise PreparationError('MODEL_PREPARATION_SCOPE_INVALID')
    inputs = private_directory(str(scope / 'inputs'), scope)
    adapter = private_directory(request['adapter_directory'], scope)
    output = private_directory(request['output_directory'], scope)
    if output != scope / 'output' or list(output.iterdir()) or adapter != inputs / 'adapter':
        raise PreparationError('MODEL_PREPARATION_OUTPUT_CONFLICT')
    exact(request['configuration'], Configuration.__dataclass_fields__)
    configuration = Configuration(**request['configuration'])
    if verification_cpu != (configuration.device == 'cpu_verification'):
        raise PreparationError('MODEL_PREPARATION_EXECUTION_PROFILE_INVALID')
    identity = request['execution_identity']
    expected = {'profile': CONTRACT['verification_profile'] if verification_cpu else CONTRACT['production_profile'],
                'python': platform.python_version(), 'worker_source_sha256': worker_source_digest()}
    if verification_cpu:
        if compact(identity) != compact(expected):
            raise PreparationError('MODEL_PREPARATION_EXECUTION_IDENTITY_REQUIRED')
    else:
        exact(identity, {*expected, 'image_id'})
        if not isinstance(identity['image_id'], str) or not re.fullmatch('sha256:[a-f0-9]{64}', identity['image_id']):
            raise PreparationError('MODEL_PREPARATION_EXECUTION_IDENTITY_REQUIRED')
        expected['image_id'] = identity['image_id']
        if compact(identity) != compact(expected):
            raise PreparationError('MODEL_PREPARATION_EXECUTION_IDENTITY_REQUIRED')
        if not scope.is_relative_to(Path(CONTRACT['container_paths']['scopes'])) or request['model_directory'] != CONTRACT['container_paths']['model']:
            raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
    exact(request['data_limits'], Limits.__dataclass_fields__)
    limits = Limits(**request['data_limits'])
    if limits.maximum_rows > RECIPE['bounds']['maximum_rows']:
        raise PreparationError('MODEL_PREPARATION_HELDOUT_INVALID')
    heldout = private_file(request['heldout_file'], limits.maximum_bytes, scope)
    if heldout != inputs / 'heldout.jsonl' or heldout.stat().st_nlink != 1:
        raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
    if not isinstance(request['adapter_files'], list) or len(request['adapter_files']) != 3:
        raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')
    for asset in request['adapter_files']:
        exact(asset, {'path', 'size_bytes', 'sha256'})
        if asset['path'] not in {'README.md', 'adapter_config.json', 'adapter_model.safetensors'} or type(asset['size_bytes']) is not int or not 1 <= asset['size_bytes'] <= RECIPE['bounds']['maximum_adapter_bytes'] or not isinstance(asset['sha256'], str) or not re.fullmatch('[a-f0-9]{64}', asset['sha256']):
            raise PreparationError('MODEL_PREPARATION_ADAPTER_INVALID')
        private_file(str(adapter / asset['path']), asset['size_bytes'], scope)
        if (adapter / asset['path']).stat().st_nlink != 1:
            raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
    exact(request['training_configuration'], TrainingConfiguration.__dataclass_fields__)
    TrainingConfiguration(**request['training_configuration'])
    if not verification_cpu and request['training_configuration']['device'] != 'nvidia_bfloat16':
        raise PreparationError('MODEL_PREPARATION_EXECUTION_PROFILE_INVALID')
    catalog = read_json(REPO / 'contracts/agent/v1/model-catalog.json', CONTRACT['maximum_catalog_bytes'])
    if catalog_digest(catalog) != request['model_catalog_sha256']:
        raise PreparationError('MODEL_PREPARATION_MODEL_INVALID')
    model = Path(request['model_directory'])
    if not model.is_absolute() or model.resolve(strict=True) != model or model.is_relative_to(scope) or scope.is_relative_to(model):
        raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
    exact(request['resources'], CONTRACT['resource_bounds'])
    for name, (minimum, maximum) in CONTRACT['resource_bounds'].items():
        value = request['resources'][name]
        if type(value) is not int or not minimum <= value <= maximum:
            raise PreparationError('MODEL_PREPARATION_RESOURCES_INVALID')
    if request['resources']['cpu_seconds'] > request['resources']['wall_seconds'] * configuration.threads:
        raise PreparationError('MODEL_PREPARATION_RESOURCES_INVALID')
    return request, configuration, limits, catalog, model, scope, output


def claim_attempt(scope):
    descriptor = os.open(scope / 'worker.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise PreparationError('MODEL_PREPARATION_PATH_INVALID')
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise PreparationError('MODEL_PREPARATION_ATTEMPT_IN_USE') from None
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def write_result(output, value, maximum):
    publish_private_json(output, 'result.json', value, maximum)
