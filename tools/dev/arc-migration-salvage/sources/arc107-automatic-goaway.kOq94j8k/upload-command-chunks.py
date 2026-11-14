from pathlib import Path
from concurrent.futures import ThreadPoolExecutor
import base64,hashlib,io,shlex,subprocess,tarfile,tempfile,uuid
base=Path(__import__('sys').argv[1]);old=max(base.glob('native-current.*'),key=lambda p:p.stat().st_mtime)
run=Path(tempfile.mkdtemp(prefix='native-commands.',dir=str(base)))
remote='/root/lane-arc107-commands-'+uuid.uuid4().hex[:12]
old_remote=(old/'remote').read_text().strip()
with tarfile.open(old/'payload.tar.gz','r:gz') as source:
 files={m.name:source.extractfile(m).read() for m in source if m.isfile()}
files['guard.sh']=files['guard.sh'].decode().replace(old_remote,remote).encode()
payload=io.BytesIO()
with tarfile.open(fileobj=payload,mode='w:gz') as target:
 for name,data in files.items():
  member=tarfile.TarInfo(name);member.size=len(data);member.mode=0o600;target.addfile(member,io.BytesIO(data))
data=payload.getvalue();(run/'payload.tar.gz').write_bytes(data)
(run/'remote').write_text(remote+'\n');(run/'tree').write_bytes((old/'tree').read_bytes())
options=['-S',(base/'ssh-control-path').read_text().strip(),'-o','ControlMaster=auto','-o','BatchMode=yes','-o','ConnectTimeout=30','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2']
def command(code,timeout=60):
 return subprocess.check_output(['ssh',*options,'ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,text=True,stderr=subprocess.STDOUT,timeout=timeout)
command("from pathlib import Path;p=Path(%r);p.mkdir(mode=0o700);f=(p/'payload.tar.gz').open('wb');f.truncate(%d);f.close()"%(remote,len(data)))
blocks=list(enumerate(range(0,len(data),4096)))
def send(item):
 index,offset=item;raw=base64.b64encode(data[offset:offset+4096]).decode()
 code="import base64;f=open(%r,'r+b');f.seek(%d);assert f.write(base64.b64decode(%r))==%d;f.close();print(%d)"%(remote+'/payload.tar.gz',offset,raw,len(data[offset:offset+4096]),index)
 last=None
 for attempt in range(3):
  try:return command(code).strip()
  except (subprocess.CalledProcessError,subprocess.TimeoutExpired) as error:last=error
 raise RuntimeError('chunk '+str(index)+' failed') from last
with ThreadPoolExecutor(max_workers=4) as pool:
 results=list(pool.map(send,blocks))
assert len(results)==len(blocks)
digest=hashlib.sha256(data).hexdigest()
actual=command("from pathlib import Path;import hashlib,tarfile;p=Path(%r);assert hashlib.sha256((p/'payload.tar.gz').read_bytes()).hexdigest()==%r;tarfile.open(p/'payload.tar.gz','r:gz').extractall(p);print('verified')"%(remote,digest))
assert actual.strip()=='verified'
(run/'verified-upload').write_text(digest+'\n')
(base/'latest-native-commands').write_text(str(run)+'\n')
print('Verified command-channel upload:',len(data),'bytes in',len(blocks),'idempotent blocks',flush=True)
with (run/'run.log').open('wb') as log:
 result=subprocess.run(['ssh',*options,'ownsolid-cpu','bash '+shlex.quote(remote+'/guard.sh')],stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,timeout=1000)
(run/'exit').write_text(str(result.returncode)+'\n')
print('Native race/build exit:',result.returncode)
print((run/'run.log').read_text()[-3000:])
raise SystemExit(result.returncode)
