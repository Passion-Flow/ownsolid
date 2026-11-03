"""Scorer-only ground truth and exact integer quality gates."""
from .configuration import CONTRACT, LABELS, PreparationError


def prediction_label(output):
    if not isinstance(output, str):
        raise PreparationError('MODEL_PREPARATION_OUTPUT_INVALID')
    value = output.strip(''.join(chr(point) for point in CONTRACT['prediction_whitespace_code_points'])).translate(str.maketrans('ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'))
    return value if value in LABELS else None


def score(expected, predictions):
    if not expected or len(expected) != len(predictions):
        raise PreparationError('MODEL_PREPARATION_REPORT_INVALID')
    confusion = [[0 for _ in range(len(LABELS) + 1)] for _ in LABELS]
    correct = 0
    for label, predicted in zip(expected, predictions):
        if label not in LABELS or (predicted is not None and predicted not in LABELS):
            raise PreparationError('MODEL_PREPARATION_REPORT_INVALID')
        confusion[LABELS.index(label)][LABELS.index(predicted) if predicted is not None else len(LABELS)] += 1
        correct += label == predicted
    return {'correct': correct, 'row_count': len(expected), 'confusion': confusion}


def gate(base_correct, candidate_correct, row_count, minimum_accuracy_basis_points, maximum_regression_basis_points):
    values = (base_correct, candidate_correct, row_count, minimum_accuracy_basis_points, maximum_regression_basis_points)
    if any(type(v) is not int for v in values) or not 1 <= row_count <= CONTRACT['bounds']['maximum_rows'] or not 0 <= base_correct <= row_count or not 0 <= candidate_correct <= row_count or not 0 <= minimum_accuracy_basis_points <= 10000 or not 0 <= maximum_regression_basis_points <= 10000:
        raise PreparationError('MODEL_PREPARATION_REPORT_INVALID')
    return candidate_correct * 10000 >= minimum_accuracy_basis_points * row_count and (base_correct - candidate_correct) * 10000 <= maximum_regression_basis_points * row_count
