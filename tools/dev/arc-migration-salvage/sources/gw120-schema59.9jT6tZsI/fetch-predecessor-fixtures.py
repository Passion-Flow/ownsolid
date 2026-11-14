from pathlib import Path
import base64,hashlib,io,json,shlex,subprocess,tarfile
p=Path(__file__).resolve().parent
code="""from pathlib import Path
import base64,hashlib,io,json,sys,tarfile,time
files={}
for v in (55,56,57,58):
 root=Path('/root/lane-arc-audit58.QQe1KRCn' if v in (55,58) else '/root/lane-arc-audit58.W1wf4RN2')
 f=root/('schema'+str(v)+'.dump');assert f.stat().st_size<4<<20;files[f.name]=f.read_bytes()
files['prefix.jsonl']=Path('/root/lane-arc-audit58.QQe1KRCn/prefix.jsonl').read_bytes()
manifest=json.dumps({n:hashlib.sha256(raw).hexdigest() for n,raw in files.items()},indent=2).encode();files['fixture-checksums.json']=manifest
out=io.BytesIO()
with tarfile.open(fileobj=out,mode='w:gz') as t:
 for name,raw in files.items():
  m=tarfile.TarInfo(name);m.mode=0o600;m.size=len(raw);t.addfile(m,io.BytesIO(raw))
encoded=base64.b64encode(out.getvalue())
for offset in range(0,len(encoded),512):
 sys.stdout.buffer.write(encoded[offset:offset+512]);sys.stdout.buffer.flush();time.sleep(.005)
"""
with (p/'predecessor-fixtures.base64').open('wb') as out,(p/'fetch-predecessors.log').open('wb') as err:
 r=subprocess.run(['ssh','-o','ConnectTimeout=25','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,stdout=out,stderr=err,timeout=120)
if r.returncode: print((p/'fetch-predecessors.log').read_text());raise SystemExit(r.returncode)
raw=base64.b64decode((p/'predecessor-fixtures.base64').read_bytes(),validate=True);(p/'predecessor-fixtures.tar.gz').write_bytes(raw)
target=p/'predecessor-fixtures';target.mkdir(mode=0o700,exist_ok=True)
with tarfile.open(fileobj=io.BytesIO(raw)) as t:
 for m in t:
  assert m.isfile() and '/' not in m.name and m.name in {'schema55.dump','schema56.dump','schema57.dump','schema58.dump','prefix.jsonl','fixture-checksums.json'}
  (target/m.name).write_bytes(t.extractfile(m).read());(target/m.name).chmod(0o600)
for name,digest in json.loads((target/'fixture-checksums.json').read_text()).items():
 assert hashlib.sha256((target/name).read_bytes()).hexdigest()==digest
 print('Verified retained fixture',name,digest)
print('Archive bytes',len(raw),'SHA-256',hashlib.sha256(raw).hexdigest())
