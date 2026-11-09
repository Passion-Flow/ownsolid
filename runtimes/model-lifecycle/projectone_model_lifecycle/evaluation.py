"""Scorer-only ground truth and exact integer quality gates."""
def prediction_label(output):
    from .configuration import CONTRACT, LABELS, PreparationError

    if not isinstance(output, str):
        raise PreparationError('MODEL_PREPARATION_OUTPUT_INVALID')
    value = output.strip(''.join(chr(point) for point in CONTRACT['prediction_whitespace_code_points'])).translate(str.maketrans('ABCDEFGHIJKLMNOPQRSTUVWXYZ', 'abcdefghijklmnopqrstuvwxyz'))
    return value if value in LABELS else None


def score(expected, predictions):
    from .configuration import LABELS, PreparationError

    try:
        return classification_score(expected, predictions, LABELS)
    except ValueError:
        raise PreparationError('MODEL_PREPARATION_REPORT_INVALID') from None


def classification_score(expected, predictions, labels):
    """Score captured labels against ground truth using integer counts."""
    if not expected or len(expected) != len(predictions):
        raise ValueError('invalid classification row count')
    confusion = [[0 for _ in range(len(labels) + 1)] for _ in labels]
    correct = 0
    for label, predicted in zip(expected, predictions):
        if label not in labels or (predicted is not None and predicted not in labels):
            raise ValueError('invalid classification label')
        confusion[labels.index(label)][labels.index(predicted) if predicted is not None else len(labels)] += 1
        correct += label == predicted
    return {'correct': correct, 'row_count': len(expected), 'confusion': confusion}


def gate(base_correct, candidate_correct, row_count, minimum_accuracy_basis_points, maximum_regression_basis_points):
    from .configuration import CONTRACT, PreparationError

    values = (base_correct, candidate_correct, row_count, minimum_accuracy_basis_points, maximum_regression_basis_points)
    if any(type(v) is not int for v in values) or not 1 <= row_count <= CONTRACT['bounds']['maximum_rows'] or not 0 <= base_correct <= row_count or not 0 <= candidate_correct <= row_count or not 0 <= minimum_accuracy_basis_points <= 10000 or not 0 <= maximum_regression_basis_points <= 10000:
        raise PreparationError('MODEL_PREPARATION_REPORT_INVALID')
    return candidate_correct * 10000 >= minimum_accuracy_basis_points * row_count and (base_correct - candidate_correct) * 10000 <= maximum_regression_basis_points * row_count
