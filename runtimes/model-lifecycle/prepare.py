"""Execute one protected offline model-preparation attempt without business credentials."""
import argparse
import errno
import json
import os
from pathlib import Path
import signal
import sys

sys.dont_write_bytecode = True
root = Path(__file__).resolve().parent
sys.path[:0] = [str(root), str(root.parent / 'training'), str(root.parent / 'data-processing')]
from projectone_model_lifecycle import VERSION
from projectone_model_lifecycle.configuration import PreparationError, compact, REPO
from projectone_model_lifecycle.boundary import CONTRACT, read_request, claim_attempt, apply_limits, write_result
from projectone_training.data import TrainingError
from projectone_model_lifecycle.inputs import hash_file


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--request-file', type=Path, required=True)
    parser.add_argument('--verification-cpu', action='store_true')
    args = parser.parse_args()
    stopped = {'requested': False}
    def stop(signum, frame):
        stopped['requested'] = True
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    previous_umask = os.umask(0o077)
    request = output = descriptor = None
    writable = False
    try:
        request, configuration, limits, catalog, model, scope, output = read_request(args.request_file, args.verification_cpu)
        request_sha256 = hash_file(args.request_file)
        descriptor = claim_attempt(scope)
        if list(output.iterdir()):
            raise PreparationError('MODEL_PREPARATION_OUTPUT_CONFLICT')
        writable = True
        enforcement = apply_limits(request['resources'], args.verification_cpu)
        sequence = 0
        event_bytes = 0
        with (scope / 'events.jsonl').open('xb') as events:
            os.chmod(events.fileno(), 0o600)
            def emit(value):
                nonlocal sequence, event_bytes
                sequence += 1
                raw = compact({'sequence': sequence, 'preparation_id': request['preparation_id'], 'attempt_id': request['attempt_id'], **value}) + b'\n'
                event_bytes += len(raw)
                if sequence > CONTRACT['maximum_events'] or len(raw) > CONTRACT['maximum_event_bytes'] or event_bytes > request['resources']['maximum_event_bytes']:
                    raise PreparationError('MODEL_PREPARATION_EVENT_LIMIT')
                events.write(raw)
                events.flush()
                os.fsync(events.fileno())
            from projectone_model_lifecycle.engine import prepare
            result = prepare(model, catalog, Path(request['adapter_directory']), request['adapter_files'], request['training_configuration'],
                Path(request['heldout_file']), request['heldout_sha256'], output, configuration, limits,
                request['resources']['maximum_output_bytes'], request['resources']['maximum_output_files'],
                lambda: stopped['requested'], emit)
        if hash_file(args.request_file) != request_sha256:
            raise PreparationError('MODEL_PREPARATION_INPUT_CHANGED')
        result.update(request_sha256=request_sha256, status='prepared', memory_enforcement=enforcement, event_count=sequence,
            execution_identity=request['execution_identity'], resources=request['resources'],
            **{key: request[key] for key in ['tenant_id', 'workspace_id', 'preparation_id', 'attempt_id', 'training_job_id', 'training_attempt_id', 'heldout_id']})
        write_result(output, result, request['resources']['maximum_result_bytes'])
        print(json.dumps({'status': 'prepared', 'preparation_id': request['preparation_id'], 'gate_passed': result['gate_passed']}))
        return 0
    except Exception as error:
        code = 'MODEL_PREPARATION_PROCESSOR_FAILED'
        if isinstance(error, PreparationError):
            code = error.code
        elif isinstance(error, TrainingError):
            code = error.code.replace('TRAINING_', 'MODEL_PREPARATION_', 1)
        elif isinstance(error, MemoryError) or type(error).__name__ == 'OutOfMemoryError':
            code = 'MODEL_PREPARATION_PROCESSOR_MEMORY_EXCEEDED'
        elif isinstance(error, FileExistsError):
            code = 'MODEL_PREPARATION_OUTPUT_CONFLICT'
        elif isinstance(error, OSError) and error.errno in {errno.ENOSPC, errno.EDQUOT}:
            code = 'MODEL_PREPARATION_STORAGE_FULL'
        elif isinstance(error, OSError) and error.errno == errno.EFBIG:
            code = 'MODEL_PREPARATION_OUTPUT_LIMIT'
        supported_codes = json.loads((REPO / 'contracts/model-lifecycle/v1/worker-errors.json').read_text())['codes']
        if code not in supported_codes:
            code = 'MODEL_PREPARATION_PROCESSOR_FAILED'
        cancelled = code == 'MODEL_PREPARATION_CANCELLED'
        status = 'cancelled' if cancelled else 'failed'
        if writable and request is not None and output is not None:
            try:
                write_result(output, {'schema_version': 1, 'worker_version': VERSION, 'status': status,
                    'preparation_id': request['preparation_id'], 'attempt_id': request['attempt_id'], 'error_code': code},
                    request['resources']['maximum_result_bytes'])
            except (PreparationError, TrainingError, OSError, ValueError):
                pass
        print(json.dumps({'status': status, 'code': code}))
        return 0 if cancelled else 1
    finally:
        signal.alarm(0)
        if descriptor is not None:
            os.close(descriptor)
        os.umask(previous_umask)


if __name__ == '__main__':
    sys.exit(main())
