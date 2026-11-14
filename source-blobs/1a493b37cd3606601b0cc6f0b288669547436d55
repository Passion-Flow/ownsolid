"""Compare two retained evaluation reports by replaying their captured predictions."""

import argparse
from pathlib import Path
import sys

from . import evaluation_safety
from .evaluation_report import ReplayError, compact, decode_object, read_bytes, replay_report, sha256, valid_digest

OUTCOMES = {
    (True, True): 'both_correct',
    (True, False): 'baseline_only',
    (False, True): 'candidate_only',
    (False, False): 'both_incorrect',
}


def _verified_report(args, side):
    mismatch = 'EVALUATION_REPORT_MISMATCH'
    try:
        capture_pin = getattr(args, f'{side}_capture_sha256')
        if not valid_digest(capture_pin):
            raise ReplayError('EVALUATION_TRUSTED_DIGEST_INVALID')
        replayed = replay_report(argparse.Namespace(
            dataset_version=args.dataset_version, dataset_version_sha256=args.dataset_version_sha256,
            validation=args.validation, recipe=args.recipe, capture_sha256=capture_pin,
            capture=getattr(args, f'{side}_capture'), model_catalog=getattr(args, f'{side}_model_catalog'),
        ))
        report = decode_object(replayed, mismatch)
        retained = read_bytes(getattr(args, f'{side}_report'), mismatch)
        if retained != replayed:
            observed = decode_object(retained, mismatch)
            for field, binding in (('dataset', 'DATASET'), ('model', 'MODEL'), ('scorer', 'SCORER'), ('cases', 'CASE')):
                if observed.get(field) != report[field]:
                    raise ReplayError(f'EVALUATION_{binding}_MISMATCH')
            raise ReplayError(mismatch)
        return report, sha256(replayed)
    except ReplayError as error:
        raise ReplayError(f'{error} [{side}]') from None


def _identity(case):
    return case['source_id'], case['prompt_sha256']


def _paired_cases(baseline, candidate):
    mismatch = 'EVALUATION_CASE_MISMATCH'
    remaining = {_identity(case): case for case in candidate}
    if len(remaining) != len(candidate):
        raise ReplayError(mismatch)
    counts = dict.fromkeys(OUTCOMES.values(), 0)
    pairs = []
    for left in baseline:
        source_id, prompt_sha256 = _identity(left)
        right = remaining.pop((source_id, prompt_sha256), None)
        expected = left['expected_label']
        if right is None or right['expected_label'] != expected:
            raise ReplayError(mismatch)
        outcome = OUTCOMES[left['predicted_label'] == expected, right['predicted_label'] == expected]
        counts[outcome] += 1
        pairs.append({
            'source_id': source_id, 'prompt_sha256': prompt_sha256, 'expected_label': expected,
            'outcome': outcome,
            'baseline': {field: left[field] for field in ('predicted_label', 'output_sha256')},
            'candidate': {field: right[field] for field in ('predicted_label', 'output_sha256')},
        })
    if remaining:
        raise ReplayError(mismatch)
    return pairs, counts


def _compare(args):
    baseline, baseline_digest = _verified_report(args, 'baseline')
    candidate, candidate_digest = _verified_report(args, 'candidate')
    if baseline['dataset'] != candidate['dataset']:
        raise ReplayError('EVALUATION_DATASET_MISMATCH')
    if baseline['scorer'] != candidate['scorer']:
        raise ReplayError('EVALUATION_SCORER_MISMATCH')
    if baseline['scorer']['recipe_id'] == evaluation_safety.SCORER_ID:
        raise ReplayError('EVALUATION_COMPARISON_SCORER_UNSUPPORTED')
    pairs, counts = _paired_cases(baseline['cases'], candidate['cases'])
    result = {
        'schema_version': 1,
        'dataset': baseline['dataset'],
        'scorer': baseline['scorer'],
        'cases': pairs,
        'pair_counts': counts,
        'delta': {'correct': counts['candidate_only'] - counts['baseline_only'], 'row_count': len(pairs)},
    }
    for side, report, digest in (('baseline', baseline, baseline_digest), ('candidate', candidate, candidate_digest)):
        result[side] = {field: report[field] for field in ('model', 'capture', 'execution_profile', 'score')}
        result[side]['report_sha256'] = digest
    return compact(result) + b'\n'


def main():
    """Write one canonical comparison or return an input-safe failure."""
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ('dataset-version', 'validation', 'recipe'):
        parser.add_argument('--' + flag, required=True, type=Path)
    parser.add_argument('--dataset-version-sha256', required=True)
    for side in ('baseline', 'candidate'):
        for flag in ('report', 'capture', 'model-catalog'):
            parser.add_argument(f'--{side}-{flag}', required=True, type=Path)
        parser.add_argument(f'--{side}-capture-sha256', required=True)
    args = parser.parse_args()
    try:
        if not valid_digest(args.dataset_version_sha256):
            raise ReplayError('EVALUATION_TRUSTED_DIGEST_INVALID')
        sys.stdout.buffer.write(_compare(args))
    except ReplayError as error:
        print(error, file=sys.stderr)
        return 1
    except (ValueError, RecursionError, OSError):
        print('EVALUATION_INPUT_INVALID', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
