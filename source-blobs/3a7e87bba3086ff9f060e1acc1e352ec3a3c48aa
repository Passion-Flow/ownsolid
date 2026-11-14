import gzip
import hashlib
import json
import pathlib
import subprocess

proof = pathlib.Path(__file__).resolve().parent
spec = json.loads((proof / 'source-delta.json').read_text())
archive = pathlib.Path('/root/ownsolid-lane-m-proof.ztWSYxrx/source.tar.gz')
delta = proof / 'source-delta.patch.gz'
if hashlib.sha256(archive.read_bytes()).hexdigest() != spec['base_archive_sha256']:
    raise SystemExit('Retained source archive differs from the measured input')
if hashlib.sha256(delta.read_bytes()).hexdigest() != spec['delta_sha256']:
    raise SystemExit('Source delta checksum mismatch')
source = proof / 'source'
source.mkdir()
subprocess.run(['tar', '-xzf', str(archive), '-C', str(source)], check=True)
patch = proof / 'source-delta.patch'
patch.write_bytes(gzip.decompress(delta.read_bytes()))
subprocess.run(['git', 'apply', '--check', str(patch)], cwd=source, check=True)
subprocess.run(['git', 'apply', str(patch)], cwd=source, check=True)
(proof / 'source-ready').write_text(spec['target_commit'] + '\n')
print('Verified retained archive and reconstructed complete source', spec['target_commit'])
