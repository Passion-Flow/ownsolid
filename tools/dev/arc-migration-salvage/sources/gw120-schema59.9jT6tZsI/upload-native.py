from pathlib import Path
import base64,hashlib,io,json,shlex,subprocess,tarfile,uuid
p=Path(__file__).resolve().parent;data=io.BytesIO()
names=['source.patch','base-paths.json','source-identity.json','prepare-source.py','formal_restore_matrix_test.go','build-offline.sh','catalog.sql','replay.sh','guard.sh']
with tarfile.open(fileobj=data,mode='w:gz') as t:
 for name in names:
  raw=(p/name).read_bytes();m=tarfile.TarInfo(name);m.mode=0o600;m.size=len(raw);t.addfile(m,io.BytesIO(raw))
raw=data.getvalue();digest=hashlib.sha256(raw).hexdigest();(p/'native-payload.tar.gz').write_bytes(raw)
remote='/root/lane-arc-gw120-schema59.'+uuid.uuid4().hex[:12]
(p/'remote').write_text(remote+'\n')
code="""from pathlib import Path
import base64,hashlib,io,subprocess,tarfile
p=Path(%r);p.mkdir(mode=0o700)
raw=base64.b64decode(%r);assert hashlib.sha256(raw).hexdigest()==%r
(p/'payload.tar.gz').write_bytes(raw)
with tarfile.open(fileobj=io.BytesIO(raw)) as t:
 for m in t:
  assert m.isfile() and '/' not in m.name and m.name in %r
  (p/m.name).write_bytes(t.extractfile(m).read());(p/m.name).chmod(0o600)
with (p/'run.log').open('wb') as log:
 child=subprocess.Popen(['bash',str(p/'guard.sh')],stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
(p/'pid').write_text(str(child.pid)+'\\n')
print('Verified payload '+%r+'; started '+str(child.pid),flush=True)
"""%(remote,base64.b64encode(raw).decode(),digest,names,digest)
options=['-o','BatchMode=yes','-o','ConnectTimeout=30','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2']
with (p/'upload.log').open('wb') as out:
 r=subprocess.run(['ssh',*options,'ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT,timeout=90)
print((p/'upload.log').read_text());print('upload exit',r.returncode,'remote',remote)
raise SystemExit(r.returncode)
