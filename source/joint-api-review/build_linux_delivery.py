import gzip,hashlib,json,pathlib,tarfile
r=pathlib.Path(__file__).parent.parent;p=r/'joint-api-review';q=r/'response-reference-audit'
files={
 'joint-source-2e64ab4c.bundle':p/'joint-source-2e64ab4c.bundle',
 'full_native_gate.prepared.py':p/'full_native_gate.prepared.py',
 'joint-full-gate-2e64ab4c-manifest.json':p/'joint-full-gate-2e64ab4c-manifest.json',
 'preparation-handoff.json':p/'handoff.json',
 'composition-proof.json':p/'composition-proof.json',
 'projection-input-sha256.json':p/'projection-input-sha256.json',
 'prepared-policy-verification.json':p/'prepared-policy-verification.json',
 'prepared-entry-vs-executed.diff':p/'prepared-entry-vs-executed.diff',
 'joint-owned-19.patch':p/'joint-owned-19.patch',
 'static-lint-diagnosis.json':p/'static-lint-diagnosis.json',
 'pointer20-verification.json':q/'verification.json',
 'pointer20-regression-tests.log':q/'audit-regression-tests.log',
 'pointer20-430-audit.json':q/'main9e-compatibility-audit.json',
 'pointer20-before-fix.json':q/'reference-reproduction-before-fix.json',
 'pointer20-read-only-receipt.json':q/'server-read-only-receipt.json',
}
expected={name:{'bytes':path.stat().st_size,'sha256':hashlib.sha256(path.read_bytes()).hexdigest()} for name,path in files.items()}
index=p/'delivery-files-sha256.json';index.write_text(json.dumps(expected,indent=2)+'\n');files['delivery-files-sha256.json']=index
packet=p/'delivery-2e64ab4c-c5e4f1cb.tar.gz'
with packet.open('xb') as output,gzip.GzipFile(fileobj=output,mode='wb',mtime=0) as compressed,tarfile.open(fileobj=compressed,mode='w|') as archive:
 for name,path in files.items():
  info=archive.gettarinfo(str(path),arcname=name);assert info.isfile();info.uid=info.gid=0;info.uname=info.gname='';info.mtime=0;info.mode=0o600
  with path.open('rb') as content:archive.addfile(info,content)
proof={'packet':str(packet),'bytes':packet.stat().st_size,'sha256':hashlib.sha256(packet.read_bytes()).hexdigest(),'files':expected,'index_sha256':hashlib.sha256(index.read_bytes()).hexdigest(),'source':'2e64ab4c6c16bd84ae5c101b9de5444550760ff2','tree':'3bdf79d7d76f096c989884190c32e4c08de20356','entry_executed':False}
(p/'delivery-packet.json').write_text(json.dumps(proof,indent=2)+'\n');print(json.dumps({k:proof[k] for k in ['packet','bytes','sha256','source','tree']},indent=2))
