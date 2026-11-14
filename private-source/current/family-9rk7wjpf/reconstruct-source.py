import gzip,hashlib,json,pathlib,shutil,subprocess
p=pathlib.Path(__file__).resolve().parent
s=json.loads((p/'source-delta.json').read_text())
base=pathlib.Path(s['base_source'])
if (base.parent/'source-ready').read_text().strip()!=s['base_commit']: raise SystemExit('Retained base target mismatch')
parts=json.loads((p/'source-parts.json').read_text())
raw=b''
for part in parts:
 if pathlib.Path(part['name']).name!=part['name']: raise SystemExit('Invalid part name')
 data=(p/part['name']).read_bytes()
 if len(data)!=part['bytes'] or hashlib.sha256(data).hexdigest()!=part['sha256']: raise SystemExit('Part checksum mismatch')
 raw+=data
if hashlib.sha256(raw).hexdigest()!=s['delta_sha256']: raise SystemExit('Complete delta checksum mismatch')
shutil.copytree(base,p/'source',symlinks=True)
patch=p/'source-delta.patch';patch.write_bytes(gzip.decompress(raw))
subprocess.run(['git','apply','--check',str(patch)],cwd=p/'source',check=True)
subprocess.run(['git','apply',str(patch)],cwd=p/'source',check=True)
(p/'source-ready').write_text(s['target_commit']+'\n')
print('Verified source delta and reconstructed',s['target_commit'])
