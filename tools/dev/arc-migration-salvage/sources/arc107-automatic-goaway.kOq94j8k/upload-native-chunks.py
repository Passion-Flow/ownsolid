from pathlib import Path
import hashlib,json,subprocess,shlex,tempfile,time
base=Path(__file__).resolve().parent;raw=(base/'native-input.tar.gz').read_bytes();manifest=json.loads((base/'native-input.json').read_text())
assert hashlib.sha256(raw).hexdigest()==manifest['archive_sha256']
root='/root/lane-arc107-linux.Ot2FdOct';socketdir=Path(tempfile.mkdtemp(prefix='arc-ssh.',dir='/private/tmp'));socket=str(socketdir/'c')
ssh=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=10','-o','ServerAliveInterval=5','-o','ServerAliveCountMax=2','-o','ControlMaster=auto','-o','ControlPersist=60','-o','ControlPath='+socket,'ownsolid-cpu']
log=(base/'native-chunk-upload.log').open('a')
def remote(code,payload=b''):
 r=subprocess.run(ssh+['python3 -c '+shlex.quote(code)],input=payload,capture_output=True,timeout=25)
 log.write(r.stdout.decode(errors='replace')+r.stderr.decode(errors='replace'));log.flush()
 if r.returncode:raise RuntimeError('Native source upload transport exited '+str(r.returncode))
 return json.loads(r.stdout)
try:
 # The prior interrupted attempt wrote no input before receiving the full stream.
 state=remote("from pathlib import Path;import json;p=Path(%r);q=p/'source.tar.gz.part';done=p/'source.tar.gz';print(json.dumps({'partial':q.stat().st_size if q.exists() else 0,'complete':done.exists()}))"%root)
 if state['complete']:
  result=remote("from pathlib import Path;import hashlib,json;p=Path(%r)/'source.tar.gz';print(json.dumps({'sha256':hashlib.sha256(p.read_bytes()).hexdigest()}))"%root)
  assert result['sha256']==manifest['archive_sha256'];print('Verified already complete native source',flush=True)
 else:
  size=65536;assert state['partial']%size==0
  for offset in range(state['partial'],len(raw),size):
   chunk=raw[offset:offset+size];digest=hashlib.sha256(chunk).hexdigest()
   code="""from pathlib import Path
import os,stat,sys,hashlib,json
p=Path(%r)/'source.tar.gz.part';offset=%r;data=sys.stdin.buffer.read()
assert len(data)==%r and hashlib.sha256(data).hexdigest()==%r
fd=os.open(p,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
try:
 info=os.fstat(fd);assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
 if info.st_size==offset:
  os.lseek(fd,offset,os.SEEK_SET)
  with os.fdopen(os.dup(fd),'wb') as out:out.write(data);out.flush();os.fsync(out.fileno())
 elif info.st_size>=offset+len(data):
  assert os.pread(fd,len(data),offset)==data
 else:raise RuntimeError('unexpected partial source length')
 print(json.dumps({'committed':offset+len(data),'chunk_sha256':hashlib.sha256(data).hexdigest()}))
finally:os.close(fd)
"""%(root,offset,len(chunk),digest)
   result=remote(code,chunk);assert result['committed']==offset+len(chunk) and result['chunk_sha256']==digest
   if (offset//size)%16==0 or offset+len(chunk)==len(raw):print('Native source verified '+str(offset+len(chunk))+'/'+str(len(raw))+' bytes',flush=True)
  code="""from pathlib import Path
import hashlib,json
root=Path(%r);p=root/'source.tar.gz.part';digest=hashlib.sha256(p.read_bytes()).hexdigest()
assert digest==%r
out=root/'source.tar.gz';assert not out.exists();p.rename(out)
(root/'input.json').write_text(%r)
print(json.dumps({'sha256':digest,'source':%r,'compiler_started':False}))
"""%(root,manifest['archive_sha256'],json.dumps(manifest,indent=2)+'\n',manifest['source'])
  result=remote(code);assert result['sha256']==manifest['archive_sha256'];print(json.dumps(result),flush=True)
finally:
 log.close()
 subprocess.run(['ssh','-o','BatchMode=yes','-o','ControlPath='+socket,'-O','exit','ownsolid-cpu'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=10)
 if not any(socketdir.iterdir()):socketdir.rmdir()
