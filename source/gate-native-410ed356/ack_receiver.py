"""Receive the source-bound lane D CLI with bounded, durable chunk ACKs."""
import datetime
import fcntl
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
import subprocess
import sys

os.umask(0o077)
cfg = json.loads(sys.argv[1])
assert cfg['source'] == '410ed356ffaf9c5a54a32d9b19e20ea457090f7b'
assert cfg['elf_sha256'] == 'e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
root = Path(cfg['root'])
assert root == Path('/root/lane-rel-tools/user-reads/client-410ed356')
directory = root / cfg['directory']
assert directory == root / 'verified-e630d9ff'
directory.mkdir(mode=0o700, exist_ok=True)
assert not directory.is_symlink() and directory.stat().st_uid == os.geteuid()
lock = os.open(directory / 'receive.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
compressed = directory / 'client.gz.part'

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def owned_regular(path):
    info = path.lstat()
    assert stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid()
    assert not info.st_mode & 0o077
    return info

def emit(value):
    value['utc'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with (directory / 'receive-events.jsonl').open('a') as output:
        output.write(json.dumps(value) + '\n')
    print(json.dumps(value), flush=True)

if not compressed.exists():
    blocks = []
    for index in (0, 1):
        part = root / 'segments-e630d9ff' / ('part-%03d' % index)
        assert owned_regular(part).st_size == 262144
        blocks.append(part.read_bytes())
    prefix = b''.join(blocks)
    assert len(prefix) == cfg['seed_size']
    assert hashlib.sha256(prefix).hexdigest() == cfg['seed_sha256']
    with compressed.open('xb') as output:
        output.write(prefix)
        output.flush()
        os.fsync(output.fileno())
info = owned_regular(compressed)
assert 0 <= info.st_size <= cfg['size']
emit({'event': 'ready', 'offset': info.st_size, 'prefix_sha256': digest(compressed)})
with compressed.open('ab') as output:
    offset = info.st_size
    chunks = 0
    while offset < cfg['size']:
        header = sys.stdin.buffer.readline(256)
        match = re.fullmatch(rb'([0-9]+) ([0-9a-f]{64})\n', header)
        assert match, 'Missing bounded chunk header'
        length = int(match[1])
        assert 0 < length <= 65536 and offset + length <= cfg['size']
        data = bytearray()
        while len(data) < length:
            part = sys.stdin.buffer.read(length - len(data))
            assert part, 'Incomplete frame; no incomplete frame written'
            data.extend(part)
        checksum = hashlib.sha256(data).hexdigest()
        assert checksum.encode() == match[2], 'Chunk checksum mismatch'
        output.write(data)
        output.flush()
        os.fsync(output.fileno())
        offset += length
        emit({'event': 'chunk', 'offset': offset, 'chunk_sha256': checksum})
        chunks += 1
        if chunks == 2 and offset < cfg['size']:
            emit({'event': 'paused', 'offset': offset, 'prefix_sha256': digest(compressed)})
            sys.exit(0)
assert digest(compressed) == cfg['gzip_sha256']
target = directory / 'ownsolid'
if not target.exists():
    staged = directory / 'elf.staged'
    assert not staged.exists(), 'Review owned staged file before retry'
    with gzip.open(compressed, 'rb') as source, staged.open('xb') as output:
        total = 0
        while data := source.read(65536):
            total += len(data)
            assert total <= cfg['elf_size']
            output.write(data)
        output.flush()
        os.fsync(output.fileno())
    assert total == cfg['elf_size'] and digest(staged) == cfg['elf_sha256']
    os.chmod(staged, 0o500)
    os.rename(staged, target)
assert owned_regular(target).st_size == cfg['elf_size'] and digest(target) == cfg['elf_sha256']
with target.open('rb') as source:
    header = source.read(64)
assert header[:6] == b'\x7fELF\x02\x01' and struct.unpack_from('<H', header, 18)[0] == 62
description = subprocess.check_output(['file', str(target)], text=True).strip()
assert 'statically linked' in description
assert cfg['client_manifest']['source_revision'] == cfg['source']
assert cfg['client_manifest']['binary_sha256'] == cfg['elf_sha256']
(directory / 'client-manifest.json').write_text(json.dumps(cfg['client_manifest'], indent=2) + '\n')
(directory / 'build-metadata.txt').write_text(cfg['build_metadata'])
receipt = {'event': 'complete', 'source': cfg['source'], 'status_source': cfg['status_source'],
           'binary_path': str(target), 'binary_bytes': cfg['elf_size'],
           'binary_sha256': digest(target), 'gzip_sha256': cfg['gzip_sha256'], 'file': description,
           'received_and_verified': True, 'native_linux_startup_run': False,
           'installed_acceptance': False,
           'boundary': 'Artifact reception only; no compilation, services or installed acceptance'}
(directory / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
emit(receipt)
