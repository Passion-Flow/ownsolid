"""Match the frozen M06 Go ModelCatalog digest independently of JSON source formatting."""
import hashlib
import json
import re
from .data import TrainingError

FIELDS = ('schema_version', 'model_key', 'repository', 'revision', 'license', 'format',
          'architecture', 'model_type', 'max_context', 'files')
ASSET_FIELDS = ('path', 'size_bytes', 'sha256')

def catalog_digest(catalog):
    if not isinstance(catalog, dict) or set(catalog) != set(FIELDS):
        raise TrainingError('TRAINING_MODEL_INVALID')
    if type(catalog['schema_version']) is not int or catalog['schema_version'] != 1 or type(catalog['max_context']) is not int or catalog['max_context'] < 1:
        raise TrainingError('TRAINING_MODEL_INVALID')
    for key in FIELDS:
        if key not in {'schema_version', 'max_context', 'files'} and (not isinstance(catalog[key], str) or not catalog[key] or len(catalog[key].encode('utf-8')) > 1024):
            raise TrainingError('TRAINING_MODEL_INVALID')
    files = catalog['files']
    if not isinstance(files, list) or not 1 <= len(files) <= 64:
        raise TrainingError('TRAINING_MODEL_INVALID')
    seen = set()
    for asset in files:
        if not isinstance(asset, dict) or set(asset) != set(ASSET_FIELDS) or not isinstance(asset['path'], str) or not re.fullmatch(r'[A-Za-z0-9_.-]{1,100}', asset['path']) or asset['path'] in {'.', '..'} or asset['path'] in seen or type(asset['size_bytes']) is not int or asset['size_bytes'] < 1 or not isinstance(asset['sha256'], str) or not re.fullmatch(r'[a-f0-9]{64}', asset['sha256']):
            raise TrainingError('TRAINING_MODEL_INVALID')
        seen.add(asset['path'])
    canonical = {key: ([{field: asset[field] for field in ASSET_FIELDS} for asset in files] if key == 'files' else catalog[key]) for key in FIELDS}
    # encoding/json escapes HTML characters and Unicode line separators even in string values.
    encoded = json.dumps(canonical, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
    for value, escaped in [('&', '\\u0026'), ('<', '\\u003c'), ('>', '\\u003e'), ('\u2028', '\\u2028'), ('\u2029', '\\u2029')]:
        encoded = encoded.replace(value, escaped)
    return hashlib.sha256(encoded.encode('utf-8')).hexdigest()
