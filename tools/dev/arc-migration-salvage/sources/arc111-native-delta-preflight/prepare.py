from pathlib import Path
import ast
import gzip
import hashlib
import io
import json
import lzma
import os
import subprocess
import tarfile

os.umask(0o077)
out = Path(__file__).resolve().parent
diagnostics = out.parent
repo = diagnostics.parent / 'lane-arc111-qualified-readers'
original = diagnostics / 'arc111-native-formal61.qtc543lk'
frozen = diagnostics / 'arc111-qualified-iam-input.rjyvuqwq'
fix = diagnostics / 'arc111-native-observed.eldemtbg/post-entry-fix'
source = '3513ab2ba5885bb5d782919f7611d96e35d28fc6'
basis = '149a95013db6c6d1ca9dbadd09b05fff9b4c98de'
base_path = '/root/lane-arc107-installed.149a9501.c92cc7f9/source'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(*args):
    return subprocess.check_output(['git', *args], cwd=repo)


def tree(rev):
    result = {}
    for entry in git('ls-tree', '-rz', rev).split(b'\0'):
        if entry:
            meta, path = entry.split(b'\t', 1)
            mode, kind, blob = meta.decode().split()
            assert kind == 'blob' and mode in {'100644', '100755'}
            result[path.decode()] = (mode, blob)
    return result


def archive_files(files):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w', format=tarfile.USTAR_FORMAT) as archive:
        for name, data, mode in sorted(files):
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = mode
            info.mtime = 0
            archive.addfile(info, io.BytesIO(data))
    return buffer.getvalue()


assert git('rev-parse', source + '^{tree}').decode().strip() == 'c65b8e3fde9833369c253c8303b70c3f13cae5d8'
manifest_bytes = (frozen / 'source-manifest.json').read_bytes()
assert digest(manifest_bytes) == '9de6cb0f1e829a6fa92a2b41b2cb9d84fbc41669805680cd7b7f0e8960ba59c9'
manifest = json.loads(manifest_bytes)
expected = {item['path']: item for item in manifest['files']}
old = tree(basis)
target = tree(source)
assert len(target) == len(expected) == 5102
assert all((item['mode'], item['git_blob']) == target[name] for name, item in expected.items())
changed = sorted(name for name in target if old.get(name) != target[name])
assert len(changed) == 240
source_archive = (frozen / 'source.tar.gz').read_bytes()
assert digest(source_archive) == '15027f9f5b3145eba29d7850f4ba5aa5eb1a7da0babaee9e875ab0600ee73848'
delta_files = []
seen = set()
with tarfile.open(fileobj=io.BytesIO(source_archive), mode='r:gz') as archive:
    for member in archive:
        if member.isdir():
            continue
        assert member.isfile() and member.name in expected and member.name not in seen
        data = archive.extractfile(member).read()
        ref = expected[member.name]
        assert digest(data) == ref['sha256'] and len(data) == ref['bytes']
        # The retained Git archive uses tar.umask=0002 (0664/0775).
        # Reconstructed source uses the exact Git regular-file modes instead.
        git_mode = int(ref['mode'], 8) & 0o777
        assert member.mode == git_mode | 0o020
        assert hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest() == ref['git_blob']
        if member.name in changed:
            delta_files.append((member.name, data, git_mode))
        seen.add(member.name)
assert seen == expected.keys()
delta = git('diff', '--binary', '--full-index', '--no-ext-diff', '--no-textconv', basis, source)
(out / 'source-delta.patch').write_bytes(delta)
basis_inputs = []
for name, (mode, blob) in sorted(old.items()):
    if name in target and target[name] == old[name]:
        continue
    data = git('cat-file', 'blob', blob)
    basis_inputs.append({'path': name, 'mode': mode, 'git_blob': blob,
                         'bytes': len(data), 'sha256': digest(data)})
binding_bytes = (original / 'binding.json').read_bytes()
binding = json.loads(binding_bytes)
transport_binding = {
    'format': 'verified-read-only-source-cache-git-delta-v1',
    'basis_source': basis, 'basis_tree': git('rev-parse', basis + '^{tree}').decode().strip(),
    'basis_path': base_path, 'basis_access': 'read-only; no fallback and no mutation',
    'target_source': source, 'target_tree': manifest['tree'],
    'target_manifest_sha256': digest(manifest_bytes), 'target_path_count': len(target),
    'delta_paths': changed, 'delta_path_count': len(changed),
    'delta_patch_sha256': digest(delta), 'delta_patch_bytes': len(delta),
    'basis_changed_or_removed_inputs': basis_inputs,
    'cached_path_count': len(target) - len(changed),
    'original_full_source_archive_sha256': digest(source_archive),
    'original_full_source_archive_transferred': False,
    'target_proof': 'All 5102 byte lengths, SHA-256, Git blob IDs and regular file modes must verify after reconstruction.',
    'native_binary_transfer': False,
}
transport_binding_bytes = (json.dumps(transport_binding, sort_keys=True, indent=2) + '\n').encode()
files = [('binding.json', binding_bytes, 0o600),
         ('transport-binding.json', transport_binding_bytes, 0o600),
         ('source-manifest.json', manifest_bytes, 0o600),
         ('source-delta.patch', delta, 0o600)]
old_payload = json.loads((original / 'payload-manifest.json').read_text())
original_wire = (original / 'payload.tar').read_bytes()
assert len(original_wire) == old_payload['bytes'] and digest(original_wire) == old_payload['sha256']
original_members = {}
with tarfile.open(fileobj=io.BytesIO(original_wire), mode='r:') as archive:
    for member in archive:
        assert member.isfile() and member.name in old_payload['members'] and member.name not in original_members
        original_members[member.name] = archive.extractfile(member).read()
assert original_members.keys() == old_payload['members'].keys()
for name, ref in old_payload['members'].items():
    data = original_members[name]
    assert len(data) == ref['bytes'] and digest(data) == ref['sha256']
    if name in {'binding.json', 'source-manifest.json', 'frozen-source.tar.gz'}:
        continue
    files.append((name, data, 0o600))
for name, data, mode in files:
    path = out / name
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    path.write_bytes(data)
raw = archive_files(files)
wire = lzma.compress(raw, format=lzma.FORMAT_XZ, check=lzma.CHECK_SHA256, preset=6)
(out / 'payload.decoded.tar').write_bytes(raw)
(out / 'payload.wire.xz').write_bytes(wire)
transport = {'encoding': 'xz-of-tar-v1', 'compression_preset': 6, 'check': 'SHA256',
             'decoded_bytes': len(raw), 'decoded_sha256': digest(raw),
             'wire_bytes': len(wire), 'wire_sha256': digest(wire),
             'source_transport': transport_binding}
payload = {'bytes': len(wire), 'sha256': digest(wire), 'transport': transport,
           'members': {name: {'bytes': len(data), 'sha256': digest(data)} for name, data, mode in files}}
(out / 'payload-manifest.json').write_text(json.dumps(payload, sort_keys=True, indent=2) + '\n')
result = {'source': source, 'tree': manifest['tree'], 'source_paths_verified_against_git_and_archive': len(target),
          'basis_source': basis, 'delta_paths': len(changed), 'cached_paths': len(target) - len(changed),
          'retained_git_archive_modes': '0664/0775; exact tar.umask=0002 mapping verified',
          'reconstructed_modes': '0644/0755 from exact Git mode entries, verified independently',
          'original_wire_bytes': old_payload['bytes'], 'original_wire_sha256': old_payload['sha256'],
          'wire_bytes': len(wire), 'wire_sha256': digest(wire),
          'decoded_bytes': len(raw), 'decoded_sha256': digest(raw),
          'required_wire_bytes_per_second45': len(wire) / 45,
          'prior_observed_channel_credit_bytes': 3487744,
          'prior_initial_credit_bytes': 2097152,
          'credit_trace_is_not_delivered_bytes_or_a_future_bandwidth_bound': True,
          'no_network_or_build_started': True}
(out / 'preparation-result.json').write_text(json.dumps(result, sort_keys=True, indent=2) + '\n')
print(json.dumps(result, indent=2))
