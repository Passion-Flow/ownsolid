"""Execute one protected training attempt; Go independently authorizes and accepts its result."""
import argparse
import errno
import json
import os
from pathlib import Path
import signal
import sys

sys.dont_write_bytecode = True
root = Path(__file__).resolve().parent
sys.path[:0] = [str(root), str(root.parent / 'data-processing')]
from projectone_training.boundary import read_request, apply_limits, write_result, claim_attempt
from projectone_training.data import TrainingError
from projectone_training import VERSION


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--request-file', type=Path, required=True)
    parser.add_argument('--verification-cpu', action='store_true')
    parser.add_argument('--verification-fixture-catalog', action='store_true')
    args = parser.parse_args()
    stopped = {'requested': False}
    def stop(signum, frame):
        stopped['requested'] = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    request = None
    output = None
    descriptor = None
    writable_result = False
    # Third-party Trainer/torch writers inherit this process-private creation policy.
    previous_umask = os.umask(0o077)
    try:
        request, configuration, limits, catalog, model, output, resume = read_request(args.request_file, root, args.verification_cpu, args.verification_fixture_catalog)
        descriptor = claim_attempt(Path(request['scope_directory']))
        if list(output.iterdir()):
            raise TrainingError('TRAINING_OUTPUT_CONFLICT')
        writable_result = True
        enforcement = apply_limits(request['resources'], args.verification_cpu)
        event_path = Path(request['scope_directory']) / 'events.jsonl'
        event_bytes = 0
        sequence = 0
        with event_path.open('xb') as events:
            os.chmod(events.fileno(), 0o600)
            def emit(value):
                nonlocal event_bytes, sequence
                sequence += 1
                raw = json.dumps({'sequence': sequence, 'job_id': request['job_id'], 'attempt_id': request['attempt_id'], **value},
                                 separators=(',', ':'), allow_nan=False).encode() + b'\n'
                event_bytes += len(raw)
                if event_bytes > request['resources']['maximum_event_bytes']:
                    raise TrainingError('TRAINING_EVENT_LIMIT')
                events.write(raw)
                events.flush()
                os.fsync(events.fileno())
            from projectone_training.engine import train
            result = train(model, catalog, Path(request['train_file']), request['train_sha256'],
                           Path(request['validation_file']), request['validation_sha256'], output, configuration, limits,
                           request['resources']['maximum_output_bytes'], request['resources']['maximum_output_files'],
                           lambda: stopped['requested'], emit, request['execution_identity'],
                           {key: request[key] for key in ['tenant_id', 'workspace_id', 'job_id']}, resume)
        result.update(schema_version=1, worker_version=VERSION, job_id=request['job_id'], attempt_id=request['attempt_id'],
                      dataset_version_id=request['dataset_version_id'], dataset_manifest_sha256=request['dataset_manifest_sha256'],
                      memory_enforcement=enforcement, event_count=sequence)
        write_result(output, result, request['resources']['maximum_result_bytes'])
        print(json.dumps({'status': result['status'], 'job_id': request['job_id'], 'attempt_id': request['attempt_id']}))
        return 0
    except Exception as error:
        code = error.code if isinstance(error, TrainingError) else 'TRAINING_PROCESSOR_MEMORY_EXCEEDED' if isinstance(error, MemoryError) else 'TRAINING_PROCESSOR_FAILED'
        if isinstance(error, OSError) and error.errno in {errno.ENOSPC, errno.EDQUOT}:
            code = 'TRAINING_STORAGE_FULL'
        if isinstance(error, OSError) and error.errno == errno.EFBIG:
            code = 'TRAINING_OUTPUT_LIMIT'
        if isinstance(error, FileExistsError):
            code = 'TRAINING_OUTPUT_CONFLICT'
        if type(error).__name__ == 'OutOfMemoryError':
            code = 'TRAINING_PROCESSOR_MEMORY_EXCEEDED'
        if writable_result and request is not None and output is not None:
            try:
                write_result(output, {'schema_version': 1, 'worker_version': VERSION, 'status': 'failed', 'job_id': request['job_id'],
                    'attempt_id': request['attempt_id'], 'error_code': code,
                    **({'partition': error.partition, 'error_line_number': error.line_number, 'cause': error.cause} if isinstance(error, TrainingError) else {})}, request['resources']['maximum_result_bytes'])
            except (TrainingError, OSError, MemoryError):
                pass
        print(json.dumps({'status': 'failed', 'code': code}))
        return 1
    finally:
        signal.alarm(0)
        if descriptor is not None:
            os.close(descriptor)
        os.umask(previous_umask)


if __name__ == '__main__':
    sys.exit(main())
