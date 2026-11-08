"""Strict private process boundary; computation receives no database/session/Hub credentials."""
import hashlib
import fcntl
import json
import os
from pathlib import Path
import platform
import re
import resource
import signal
import stat
import sys
from projectone_data.boundary import private_directory, private_file, valid_id
from projectone_data.validation import DataError, Limits, unique, invalid_constant
from .checkpoints import CheckpointReference, file_digest, publish_private_json
from .configuration import Configuration, runtime_contract
from .data import TrainingError
from .catalog import catalog_digest
from .processor_generated import PROCESSOR_CONTRACT as CONTRACT


def exact(value, fields):
    if not isinstance(value, dict) or set(value) != set(fields):
        raise TrainingError('TRAINING_REQUEST_INVALID')


def decoded(path, maximum, scope=None):
    try:
        path = private_file(str(path), maximum, scope)
        if path.stat().st_nlink != 1:
            raise TrainingError('TRAINING_PATH_INVALID')
        return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=unique, parse_constant=invalid_constant)
    except (OSError, DataError, ValueError, TypeError, RecursionError):
        raise TrainingError('TRAINING_REQUEST_INVALID') from None


def digest(value):
    if not isinstance(value, str) or not re.fullmatch('[a-f0-9]{64}', value):
        raise TrainingError('TRAINING_REQUEST_INVALID')
    return value


def worker_source_digest(root):
    files = {str(path.relative_to(root.parent)): file_digest(path)
             for directory in [root, root.parent / 'data-processing']
             for path in sorted(directory.rglob('*.py'))
             if not {'__pycache__', 'tests'}.intersection(path.relative_to(directory).parts)}
    return hashlib.sha256(json.dumps(files, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def read_installed_catalog(root):
    release = root.parents[1]
    for contract_root in ('api', 'contracts'):
        path = release / contract_root / 'agent/v1/model-catalog.json'
        try:
            info = path.lstat()
        except FileNotFoundError:
            continue
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_size > CONTRACT['maximum_catalog_bytes']:
            raise TrainingError('TRAINING_MODEL_INVALID')
        return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=unique, parse_constant=invalid_constant)
    raise TrainingError('TRAINING_MODEL_INVALID')


def read_request(path, root, verification_cpu, fixture_catalog):
    try:
        return validated_request(path, root, verification_cpu, fixture_catalog)
    except (DataError, OSError, ValueError, TypeError, KeyError, RecursionError):
        raise TrainingError('TRAINING_REQUEST_INVALID') from None


def validated_request(path, root, verification_cpu, fixture_catalog):
    runtime_contract()
    request = decoded(path, CONTRACT['maximum_request_bytes'])
    exact(request, CONTRACT['request_fields'])
    if type(request['schema_version']) is not int or request['schema_version'] != 1 or request['operation'] != 'train':
        raise TrainingError('TRAINING_REQUEST_INVALID')
    for key in ['tenant_id', 'workspace_id', 'job_id', 'attempt_id', 'dataset_version_id']:
        if not valid_id(request[key]):
            raise TrainingError('TRAINING_REQUEST_INVALID')
    for key in ['dataset_manifest_sha256', 'model_catalog_sha256', 'train_sha256', 'validation_sha256']:
        digest(request[key])
    for key in ['scope_directory', 'model_directory', 'train_file', 'validation_file', 'output_directory']:
        if not isinstance(request[key], str) or len(request[key].encode('utf-8', errors='strict')) > CONTRACT['maximum_path_bytes']:
            raise TrainingError('TRAINING_PATH_INVALID')
    try:
        scope = private_directory(request['scope_directory'])
        private_file(str(path), CONTRACT['maximum_request_bytes'], scope)
        if scope.name != request['attempt_id'] or scope.parent.name != request['job_id']:
            raise TrainingError('TRAINING_SCOPE_INVALID')
        marker = decoded(scope / CONTRACT['marker_name'], CONTRACT['maximum_marker_bytes'], scope)
        expected_marker = {key: request[key] for key in ['schema_version', 'tenant_id', 'workspace_id', 'job_id', 'attempt_id', 'dataset_version_id']}
        if not isinstance(marker, dict) or type(marker.get('schema_version')) is not int or marker != expected_marker:
            raise TrainingError('TRAINING_SCOPE_INVALID')
        inputs = private_directory(str(scope / 'inputs'), scope)
        output = private_directory(request['output_directory'], scope)
        if output != scope / 'output' or list(output.iterdir()):
            raise TrainingError('TRAINING_OUTPUT_CONFLICT')
        exact(request['data_limits'], Limits.__dataclass_fields__)
        limits = Limits(**request['data_limits'])
        for key, filename in [('train_file', 'train.jsonl'), ('validation_file', 'validation.jsonl')]:
            file = private_file(request[key], limits.maximum_bytes, scope)
            if file != inputs / filename or file.stat().st_nlink != 1:
                raise TrainingError('TRAINING_PATH_INVALID')
    except (OSError, DataError, TypeError, ValueError):
        raise TrainingError('TRAINING_PATH_INVALID') from None
    exact(request['configuration'], Configuration.__dataclass_fields__)
    configuration = Configuration(**request['configuration'])
    if verification_cpu != (configuration.device == CONTRACT['verification_profile']):
        raise TrainingError('TRAINING_EXECUTION_PROFILE_INVALID')
    if fixture_catalog and not verification_cpu or not verification_cpu and request['fixture_catalog_file'] is not None:
        raise TrainingError('TRAINING_EXECUTION_PROFILE_INVALID')
    if verification_cpu:
        exact(request['execution_identity'], {'profile', 'python', 'worker_source_sha256'})
        if request['execution_identity'] != {'profile': 'native_cpu_verification', 'python': platform.python_version(),
                                             'worker_source_sha256': worker_source_digest(root)}:
            raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    else:
        exact(request['execution_identity'], {'profile', 'python', 'worker_source_sha256', 'image_id'})
        if request['execution_identity']['profile'] != CONTRACT['production_profile'] or request['execution_identity']['python'] != platform.python_version() or request['execution_identity']['worker_source_sha256'] != worker_source_digest(root) or not re.fullmatch('sha256:[a-f0-9]{64}', request['execution_identity']['image_id']):
            raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    if fixture_catalog:
        catalog_file = private_file(request['fixture_catalog_file'], CONTRACT['maximum_catalog_bytes'], scope)
        if catalog_file != inputs / 'synthetic-catalog.json':
            raise TrainingError('TRAINING_PATH_INVALID')
        catalog = decoded(catalog_file, CONTRACT['maximum_catalog_bytes'], scope)
    else:
        if request['fixture_catalog_file'] is not None:
            raise TrainingError('TRAINING_EXECUTION_PROFILE_INVALID')
        catalog = read_installed_catalog(root)
    if catalog_digest(catalog) != request['model_catalog_sha256']:
        raise TrainingError('TRAINING_MODEL_CHANGED')
    model = Path(request['model_directory'])
    if not model.is_absolute() or model.resolve(strict=True) != model or model.is_relative_to(scope) and not fixture_catalog or scope.is_relative_to(model):
        raise TrainingError('TRAINING_MODEL_INVALID')
    exact(request['resources'], CONTRACT['resource_bounds'])
    for name, (minimum, maximum) in CONTRACT['resource_bounds'].items():
        if type(request['resources'][name]) is not int or not minimum <= request['resources'][name] <= maximum:
            raise TrainingError('TRAINING_RESOURCES_INVALID')
    if request['resources']['cpu_seconds'] > request['resources']['wall_seconds'] * configuration.threads:
        raise TrainingError('TRAINING_RESOURCES_INVALID')
    resume = request['resume_checkpoint']
    if resume is not None:
        exact(resume, {'directory', 'receipt_sha256'})
        digest(resume['receipt_sha256'])
        directory = private_directory(resume['directory'], scope)
        if directory != inputs / 'checkpoint':
            raise TrainingError('TRAINING_PATH_INVALID')
        resume = CheckpointReference(directory, resume['receipt_sha256'])
    return request, configuration, limits, catalog, model, output, resume


def claim_attempt(scope):
    descriptor = os.open(scope / 'worker.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_mode & 0o077 or info.st_nlink != 1:
            raise TrainingError('TRAINING_PATH_INVALID')
        try:
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise TrainingError('TRAINING_ATTEMPT_IN_USE') from None
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def apply_limits(values, verification_cpu):
    def timeout(signum, frame):
        raise TrainingError('TRAINING_PROCESSOR_TIMEOUT')
    def output_limit(signum, frame):
        raise TrainingError('TRAINING_OUTPUT_LIMIT')
    if verification_cpu:
        mode = 'native_cpu_fsize_wall_parent_RSS_required_verification'
    else:
        if sys.platform != 'linux' or platform.machine() != 'x86_64' or os.geteuid() == 0:
            raise TrainingError('TRAINING_PLATFORM_UNSUPPORTED')
        try:
            memory = int(Path('/sys/fs/cgroup/memory.max').read_text().strip())
            if not 1 <= memory <= values['memory_bytes']:
                raise ValueError('cgroup')
        except (OSError, ValueError):
            raise TrainingError('TRAINING_MEMORY_BOUNDARY_REQUIRED') from None
        mode = 'linux_cgroup_memory_cpu_fsize_wall'
    signal.signal(signal.SIGALRM, timeout)
    signal.alarm(values['wall_seconds'])
    signal.signal(signal.SIGXCPU, timeout)
    signal.signal(signal.SIGXFSZ, output_limit)
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    resource.setrlimit(resource.RLIMIT_CPU, (values['cpu_seconds'], values['cpu_seconds'] + 1))
    resource.setrlimit(resource.RLIMIT_FSIZE, (max(values['maximum_output_bytes'], values['maximum_result_bytes'], values['maximum_event_bytes']),) * 2)
    resource.setrlimit(resource.RLIMIT_NOFILE, (values['open_files'], values['open_files']))
    return mode


def write_result(output, value, maximum):
    publish_private_json(output, 'result.json', value, maximum)
