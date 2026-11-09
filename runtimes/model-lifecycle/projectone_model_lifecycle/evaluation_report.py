"""Replay a captured base-model classification report on frozen offline inputs."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
from uuid import UUID

from projectone_data import SCHEMA
from projectone_data.limits_generated import LIMIT_CEILINGS
from projectone_data.validation import DataError, Limits, inspect_file, invalid_constant, unique
from projectone_training.catalog import TrainingError, catalog_digest

from . import VERSION, evaluation

# The preparation processor caps result.json at 8 MiB. The same bounded read
# covers the smaller version, catalog, recipe and canonical report files.
MAX_JSON_BYTES = 8 * 1024 * 1024
MIN_CASES, MAX_CASES = 3, 256
SCORER_ID = 'support_routing_v1'
DIGEST = re.compile(r'[0-9a-f]{64}')


class ReplayError(ValueError):
    """A stable, input-safe replay failure."""


def compact(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(',', ':'),
                      allow_nan=False).encode('utf-8')


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def valid_digest(value):
    return isinstance(value, str) and DIGEST.fullmatch(value) is not None


def valid_id(value):
    if not isinstance(value, str):
        return False
    try:
        parsed = UUID(value)
    except ValueError:
        return False
    return parsed.int != 0 and str(parsed) == value


def read_bytes(path, error):
    try:
        if not path.is_file():
            raise ReplayError(error)
        with path.open('rb') as file:
            raw = file.read(MAX_JSON_BYTES + 1)
    except OSError:
        raise ReplayError(error) from None
    if not raw or len(raw) > MAX_JSON_BYTES:
        raise ReplayError(error)
    return raw


def read_object(path, error, pinned_sha256=None):
    raw = read_bytes(path, error)
    if pinned_sha256 is not None and sha256(raw) != pinned_sha256:
        raise ReplayError(error)
    return decode_object(raw, error)


def decode_object(raw, error):
    """Decode an offline report object and reject ambiguous JSON."""
    try:
        value = json.loads(raw.decode('utf-8'), object_pairs_hook=unique, parse_constant=invalid_constant)
    except (ValueError, DataError, RecursionError):
        raise ReplayError(error) from None
    if not isinstance(value, dict):
        raise ReplayError(error)
    return value


def frozen_cases(version, validation, capture):
    error = 'EVALUATION_DATASET_MISMATCH'
    if not all(valid_id(version.get(field)) for field in ('id', 'dataset_id', 'workspace_id')):
        raise ReplayError(error)
    if capture.get('workspace_id') != version['workspace_id']:
        raise ReplayError('EVALUATION_WORKSPACE_MISMATCH')
    manifest = version.get('manifest')
    if (not isinstance(manifest, dict) or type(manifest.get('schema_version')) is not int
            or manifest['schema_version'] not in (1, 2) or manifest.get('row_schema') != SCHEMA):
        raise ReplayError(error)
    assets = manifest.get('files')
    reference = manifest.get('manifest_file')
    manifest_sha256 = version.get('manifest_sha256')
    if (not valid_digest(manifest_sha256) or not isinstance(reference, dict)
            or reference.get('path') != 'manifest.json' or reference.get('sha256') != manifest_sha256
            or not valid_digest(manifest.get('source_sha256'))
            or version.get('effective_source_sha256') != manifest['source_sha256']
            or not isinstance(assets, list) or not all(isinstance(asset, dict) for asset in assets)):
        raise ReplayError(error)
    matches = [asset for asset in assets if asset.get('path') == 'validation.jsonl']
    count = manifest.get('validation_rows')
    if len(matches) != 1 or type(count) is not int or not MIN_CASES <= count <= MAX_CASES:
        raise ReplayError(error)
    asset = matches[0]
    if not valid_digest(asset.get('sha256')) or capture.get('heldout_sha256') != asset['sha256']:
        raise ReplayError(error)
    limits = Limits(maximum_bytes=LIMIT_CEILINGS['maximum_bytes'][1], maximum_rows=MAX_CASES,
                    maximum_line_bytes=LIMIT_CEILINGS['maximum_line_bytes'][1],
                    maximum_text_bytes=LIMIT_CEILINGS['maximum_text_bytes'][1],
                    maximum_source_bytes=LIMIT_CEILINGS['maximum_source_bytes'][1], maximum_errors=1)
    try:
        if not validation.is_file():
            raise ReplayError(error)
        inspected = inspect_file(validation, limits)
    except (DataError, OSError):
        raise ReplayError(error) from None
    if (not inspected.valid() or len(inspected.rows) != count
            or inspected.source_sha256 != asset['sha256']
            or type(asset.get('size_bytes')) is not int or inspected.source_bytes != asset['size_bytes']):
        raise ReplayError(error)
    return inspected.rows


def captured_profile(capture, recipe):
    error = 'EVALUATION_PROFILE_MISMATCH'
    config = capture.get('configuration')
    if not isinstance(config, dict) or capture.get('configuration_sha256') != sha256(compact(config)):
        raise ReplayError(error)
    device = config.get('device')
    seed = config.get('seed')
    threads = config.get('threads')
    input_tokens = config.get('maximum_input_tokens')
    new_tokens = config.get('maximum_new_tokens')
    maximum_context = recipe.get('maximum_context_tokens')
    dtype = {'cpu_verification': 'float32', 'nvidia_bfloat16': 'bfloat16'}
    if (not isinstance(device, str) or device not in dtype or capture.get('device_profile') != device
            or capture.get('computation_dtype') != dtype[device] or type(seed) is not int
            or not 0 <= seed <= 2147483647 or type(threads) is not int or not 1 <= threads <= 8
            or type(input_tokens) is not int or not 64 <= input_tokens <= 1984
            or type(new_tokens) is not int or not 1 <= new_tokens <= 64
            or type(maximum_context) is not int or input_tokens + new_tokens > maximum_context):
        raise ReplayError(error)
    packages = capture.get('packages')
    identity = capture.get('execution_identity')
    template = capture.get('chat_template_sha256')
    instruction = capture.get('instruction_sha256')
    generation = recipe.get('generation')
    if (not isinstance(packages, dict) or not packages
            or not all(isinstance(value, str) and value for value in packages.values())
            or not isinstance(identity, dict) or not valid_digest(identity.get('worker_source_sha256'))
            or not isinstance(identity.get('python'), str) or not identity['python']
            or not valid_digest(template) or not valid_digest(instruction)
            or not isinstance(recipe.get('instruction'), str)
            or instruction != sha256(recipe['instruction'].encode('utf-8'))
            or not isinstance(generation, dict)):
        raise ReplayError(error)
    image = identity.get('image_id')
    if device == 'cpu_verification':
        if identity.get('profile') != 'native_cpu_verification' or image is not None:
            raise ReplayError(error)
    elif identity.get('profile') != device or not isinstance(image, str) or not image.startswith('sha256:') or not valid_digest(image[7:]):
        raise ReplayError(error)
    return {
        'mode': 'captured_output_replay', 'fresh_inference': 'unverified',
        'device': device, 'computation_dtype': dtype[device], 'seed': seed, 'threads': threads,
        'generation': generation, 'batch_size': 1,
        'maximum_input_tokens': input_tokens, 'maximum_new_tokens': new_tokens,
        'configuration_sha256': capture['configuration_sha256'],
        'execution_identity': identity, 'packages': packages,
        'chat_template_sha256': template, 'instruction_sha256': instruction,
    }


def replay_cases(rows, capture, labels):
    error = 'EVALUATION_CASE_MISMATCH'
    cases = capture.get('cases')
    count = capture.get('row_count')
    if (not isinstance(cases, list) or type(count) is not int or count != len(rows)
            or len(cases) != len(rows) or {row.completion for row in rows} != set(labels)):
        raise ReplayError(error)
    seen = set()
    observations = []
    for row, case in zip(rows, cases, strict=True):
        prompt_sha256 = sha256(row.prompt.encode('utf-8'))
        identity = (row.source_id, prompt_sha256)
        if (identity in seen or not isinstance(case, dict)
                or (case.get('source_id'), case.get('prompt_sha256')) != identity
                or case.get('expected_label') != row.completion):
            raise ReplayError(error)
        seen.add(identity)
        prediction = case.get('base')
        if (not isinstance(prediction, dict) or 'predicted_label' not in prediction
                or not valid_digest(prediction.get('output_sha256'))):
            raise ReplayError(error)
        observations.append({
            'source_id': row.source_id, 'prompt_sha256': prompt_sha256,
            'expected_label': row.completion, 'predicted_label': prediction['predicted_label'],
            'output_sha256': prediction['output_sha256'],
        })
    try:
        scores = evaluation.classification_score(
            [row.completion for row in rows], [item['predicted_label'] for item in observations], labels,
        )
    except ValueError:
        raise ReplayError(error) from None
    if compact(capture.get('base')) != compact(scores):
        raise ReplayError('EVALUATION_CAPTURE_SCORE_MISMATCH')
    return observations, scores


def replay_report(args):
    version = read_object(args.dataset_version, 'EVALUATION_DATASET_MISMATCH', args.dataset_version_sha256)
    capture = read_object(args.capture, 'EVALUATION_CAPTURE_MISMATCH', args.capture_sha256)
    if (type(capture.get('schema_version')) is not int or capture['schema_version'] != 1
            or capture.get('status') != 'prepared' or capture.get('worker_version') != VERSION
            or not all(valid_id(capture.get(field)) for field in ('attempt_id', 'heldout_id'))):
        raise ReplayError('EVALUATION_CAPTURE_MISMATCH')
    recipe = read_object(args.recipe, 'EVALUATION_SCORER_MISMATCH')
    recipe_sha256 = sha256(compact(recipe))
    labels = recipe.get('labels')
    if (type(recipe.get('schema_version')) is not int or recipe['schema_version'] != 1
            or recipe.get('recipe_id') != SCORER_ID or capture.get('recipe_id') != SCORER_ID
            or capture.get('recipe_sha256') != recipe_sha256
            or labels != ['account', 'billing', 'technical']):
        raise ReplayError('EVALUATION_SCORER_MISMATCH')
    catalog = read_object(args.model_catalog, 'EVALUATION_MODEL_MISMATCH')
    try:
        model_sha256 = catalog_digest(catalog)
    except TrainingError:
        raise ReplayError('EVALUATION_MODEL_MISMATCH') from None
    if capture.get('model_catalog_sha256') != model_sha256:
        raise ReplayError('EVALUATION_MODEL_MISMATCH')

    rows = frozen_cases(version, args.validation, capture)
    profile = captured_profile(capture, recipe)
    observations, scores = replay_cases(rows, capture, labels)
    return compact({
        'schema_version': 1,
        'dataset': {
            'id': version['dataset_id'], 'version_id': version['id'], 'workspace_id': version['workspace_id'],
            'version_sha256': args.dataset_version_sha256, 'manifest_sha256': version['manifest_sha256'],
            'validation_sha256': capture['heldout_sha256'],
        },
        'model': {'repository': catalog['repository'], 'revision': catalog['revision'], 'catalog_sha256': model_sha256},
        'scorer': {
            'recipe_id': SCORER_ID, 'recipe_sha256': recipe_sha256, 'labels': labels,
            'implementation_sha256': sha256(read_bytes(Path(evaluation.__file__), 'EVALUATION_SCORER_MISMATCH')),
        },
        'capture': {
            'sha256': args.capture_sha256, 'attempt_id': capture['attempt_id'], 'heldout_id': capture['heldout_id'],
            'worker_version': capture['worker_version'], 'model_role': 'base',
        },
        'execution_profile': profile, 'score': scores, 'cases': observations,
    }) + b'\n'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ('dataset-version', 'validation', 'capture', 'model-catalog', 'recipe'):
        parser.add_argument('--' + flag, required=True, type=Path)
    parser.add_argument('--dataset-version-sha256', required=True)
    parser.add_argument('--capture-sha256', required=True)
    parser.add_argument('--check-report', type=Path)
    args = parser.parse_args()
    try:
        if not valid_digest(args.dataset_version_sha256) or not valid_digest(args.capture_sha256):
            raise ReplayError('EVALUATION_TRUSTED_DIGEST_INVALID')
        rendered = replay_report(args)
        if args.check_report is not None and read_bytes(args.check_report, 'EVALUATION_REPORT_MISMATCH') != rendered:
            raise ReplayError('EVALUATION_REPORT_MISMATCH')
        sys.stdout.buffer.write(rendered)
    except ReplayError as error:
        print(error, file=sys.stderr)
        return 1
    except (ValueError, RecursionError, OSError):
        print('EVALUATION_INPUT_INVALID', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
