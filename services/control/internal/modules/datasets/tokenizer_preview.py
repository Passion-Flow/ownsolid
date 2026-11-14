"""Transport one bounded dataset request to the installed training tokenizer."""
from dataclasses import asdict
import json
from pathlib import Path
import sys

# These roots and the private request path come from the control process.
data_root, training_root, request_path = sys.argv[1:]
sys.path[:0] = [data_root, training_root]
from projectone_data.boundary import apply_limits, exact, private_directory, private_file, write_result
from projectone_data.validation import DataError, Row, invalid_constant, unique
from projectone_training.data import TrainingError
from projectone_training.preview import preview_samples

MAXIMUM_ENVELOPE_BYTES = 512 * 1024


def main():
    path = Path(request_path)
    scope = private_directory(str(path.parent))
    try:
        source = private_file(str(path), MAXIMUM_ENVELOPE_BYTES, scope)
        request = json.loads(source.read_text(encoding='utf-8'), object_pairs_hook=unique,
                             parse_constant=invalid_constant)
        exact(request, {'model_catalog_sha256', 'maximum_tokens', 'rows', 'resources'})
        apply_limits(request['resources'])
        rows = [Row(**row) for row in request['rows']]
        model = private_directory(str(scope / 'model'), scope)
        preview = asdict(preview_samples(model, request['model_catalog_sha256'], rows,
                                         request['maximum_tokens']))
        for sample in preview['samples']:
            if sample['first_excess_token_index'] is None:
                del sample['first_excess_token_index'], sample['first_excess_section']
        write_result(scope, {'preview': preview}, MAXIMUM_ENVELOPE_BYTES)
        return 0
    except (DataError, TrainingError) as error:
        write_result(scope, {'error_code': error.code}, MAXIMUM_ENVELOPE_BYTES)
        return 1


if __name__ == '__main__':
    sys.exit(main())
