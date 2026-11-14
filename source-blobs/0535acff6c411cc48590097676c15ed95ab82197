"""Validate the trusted worker boundary and emit stable codes without source diagnostics."""
import hashlib
import json
import math
import os
from pathlib import Path
import stat
import sys

from .contract_generated import CATALOG, CONTRACT


class Failure(Exception):
    pass


def fail(code):
    raise Failure(code)


def exact(value, names, code='KNOWLEDGE_WORKER_REQUEST_INVALID'):
    if not isinstance(value, dict) or set(value) != set(names):
        fail(code)


def integer(value, minimum, maximum):
    return type(value) is int and minimum <= value <= maximum


def decode(raw):
    def pairs(values):
        out = {}
        for key, value in values:
            if key in out:
                fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
            out[key] = value
        return out
    try:
        return json.loads(raw, object_pairs_hook=pairs, parse_constant=lambda _: fail('KNOWLEDGE_WORKER_REQUEST_INVALID'))
    except (UnicodeError, ValueError):
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')


def request(raw):
    value = decode(raw)
    exact(value, CONTRACT['requests']['common'])
    if value['schema_version'] != CONTRACT['schema_version'] or type(value['schema_version']) is not int or value['operation'] not in CONTRACT['requests']['operation'] or value['encoder_catalog_sha256'] != CONTRACT['encoder_catalog_sha256']:
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
    limits, resources = value['limits'], value['resources']
    exact(limits, CONTRACT['requests']['limits'])
    exact(resources, CONTRACT['requests']['resources'])
    if not (integer(limits['chunk_bytes'], 1024, CONTRACT['maximum_chunk_bytes']) and integer(limits['source_bytes'], 1, CONTRACT['maximum_source_bytes']) and integer(limits['source_tokens'], 1, CONTRACT['maximum_source_tokens']) and integer(limits['chunks'], 1, CONTRACT['maximum_chunks']) and integer(limits['chunk_tokens'], 1, CONTRACT['maximum_chunk_tokens']) and integer(limits['overlap_tokens'], 0, limits['chunk_tokens'] - 1) and integer(limits['query_tokens'], 1, CONTRACT['maximum_chunk_tokens']) and integer(resources['threads'], 1, 4) and integer(resources['batch_size'], 1, 32)):
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
    text = value['text']
    if not isinstance(text, str):
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
    try:
        encoded = text.encode('utf-8')
    except UnicodeError:
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
    if not text.strip():
        fail('KNOWLEDGE_EMPTY_SOURCE')
    if '\0' in text or any(ord(c) < 32 and c not in '\t\r\n' for c in text):
        fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
    if len(encoded) > limits['source_bytes']:
        fail('KNOWLEDGE_SOURCE_LIMIT_EXCEEDED')
    root = value['model_directory']
    if not isinstance(root, str) or not Path(root).is_absolute():
        fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
    root = Path(root)
    try:
        info = root.lstat()
        if root.resolve(strict=True) != root or not stat.S_ISDIR(info.st_mode) or info.st_uid not in [0, os.geteuid()] or info.st_mode & 0o022:
            fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
        expected = {item['path'] for item in CATALOG['files']}
        seen = set()
        for parent, directories, files in os.walk(root, followlinks=False):
            for name in directories:
                path = Path(parent) / name
                mode = path.lstat()
                if path.is_symlink() or mode.st_mode & 0o022 or mode.st_uid not in [0, os.geteuid()]:
                    fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
            for name in files:
                seen.add(str((Path(parent) / name).relative_to(root)))
        if seen != expected:
            fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
        for item in CATALOG['files']:
            path = root / item['path']
            info = path.lstat()
            if path.resolve(strict=True) != path or not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid not in [0, os.geteuid()] or info.st_mode & 0o022 or info.st_size != item['size_bytes']:
                fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
            digest = hashlib.sha256()
            with path.open('rb') as stream:
                for block in iter(lambda: stream.read(1048576), b''):
                    digest.update(block)
            if digest.hexdigest() != item['sha256']:
                fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
    except OSError:
        fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
    return value


def validate_result(value, source):
    exact(value, CONTRACT['result']['common'], 'KNOWLEDGE_WORKER_RESULT_INVALID')
    raw = source['text'].encode('utf-8')
    if value['schema_version'] != CONTRACT['schema_version'] or value['encoder_catalog_sha256'] != CONTRACT['encoder_catalog_sha256'] or value['dimension'] != CONTRACT['dimension'] or value['source_sha256'] != hashlib.sha256(raw).hexdigest() or not isinstance(value['chunks'], list) or not 1 <= len(value['chunks']) <= source['limits']['chunks']:
        fail('KNOWLEDGE_WORKER_RESULT_INVALID')
    previous = -1
    for ordinal, chunk in enumerate(value['chunks']):
        exact(chunk, CONTRACT['result']['chunk'], 'KNOWLEDGE_WORKER_RESULT_INVALID')
        if chunk['ordinal'] != ordinal or type(chunk['ordinal']) is not int or not integer(chunk['start_byte'], 0, len(raw) - 1) or not integer(chunk['end_byte'], chunk['start_byte'] + 1, len(raw)) or chunk['end_byte']-chunk['start_byte'] > source['limits']['chunk_bytes'] or chunk['start_byte'] < previous or not integer(chunk['token_count'], 1, source['limits']['query_tokens'] if source['operation'] == 'query' else source['limits']['chunk_tokens']):
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        try:
            raw[chunk['start_byte']:chunk['end_byte']].decode('utf-8')
        except UnicodeError:
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        vector = chunk['embedding']
        if not isinstance(vector, list) or len(vector) != CONTRACT['dimension'] or any(type(v) not in [int, float] or not math.isfinite(v) for v in vector):
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        if abs(math.sqrt(sum(v*v for v in vector)) - 1) > CONTRACT['vector_norm_tolerance']:
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        previous = chunk['start_byte']
    if value['chunks'][0]['start_byte'] != 0 or value['chunks'][-1]['end_byte'] != len(raw) or source['operation'] == 'query' and len(value['chunks']) != 1:
        fail('KNOWLEDGE_WORKER_RESULT_INVALID')


def main():
    try:
        if len(sys.argv) != 1:
            fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
        raw = sys.stdin.buffer.read(CONTRACT['maximum_request_bytes'] + 1)
        if not raw or len(raw) > CONTRACT['maximum_request_bytes']:
            fail('KNOWLEDGE_WORKER_REQUEST_INVALID')
        value = request(raw)
        from .engine import encode
        result = encode(value)
        validate_result(result, value)
        raw = json.dumps(result, ensure_ascii=False, allow_nan=False, separators=(',', ':')).encode('utf-8')
        if len(raw) > CONTRACT['maximum_result_bytes']:
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        sys.stdout.buffer.write(raw + b'\n')
        sys.stdout.buffer.flush()
    except Failure as error:
        code = str(error)
        if code not in CONTRACT['errors']:
            code = 'KNOWLEDGE_WORKER_FAILED'
        sys.stderr.write(json.dumps({'error': code}) + '\n')
        raise SystemExit(1) from None
    except Exception:
        sys.stderr.write(json.dumps({'error': 'KNOWLEDGE_WORKER_FAILED'}) + '\n')
        raise SystemExit(1) from None
