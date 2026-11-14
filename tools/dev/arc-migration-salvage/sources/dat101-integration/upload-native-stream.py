from pathlib import Path
import base64,hashlib,json,os,select,shlex,subprocess,time
base=Path(__file__).resolve().parent
root=json.loads((base/'native-handoff-root.json').read_text())['root']
name='dat101-schema60-f1107579-minimal.bundle';data=(base/name).read_bytes()
remote='''from pathlib import Path
import base64,hashlib,json,os,stat,sys
p=Path(ROOT)/NAME
assert p.parent.is_dir() and not p.parent.is_symlink() and p.parent.stat().st_uid==os.geteuid()
fd=os.open(p,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600)
assert stat.S_ISREG(os.fstat(fd).st_mode) and os.fstat(fd).st_uid==os.geteuid()
f=os.fdopen(fd,'r+b');old=f.read()
print(json.dumps({'size':len(old),'sha256':hashlib.sha256(old).hexdigest()}),flush=True)
for line in sys.stdin:
 message=json.loads(line)
 if 'done' in message:
  f.seek(0);raw=f.read();assert len(raw)==message['size'] and hashlib.sha256(raw).hexdigest()==message['sha256'];print('DONE '+hashlib.sha256(raw).hexdigest(),flush=True);break
 f.seek(0);before=f.read();assert len(before)==message['offset'] and hashlib.sha256(before).hexdigest()==message['prefix']
 f.write(base64.b64decode(message['data']));f.flush();os.fsync(f.fileno());f.seek(0);after=f.read()
 print(str(len(after))+' '+hashlib.sha256(after).hexdigest(),flush=True)
f.close()
print('LOCAL_RUNTIME '+', '.join(str(p) for p in Path('/usr/local/bin').glob('python*')),flush=True)
print('LOCAL_ARROW '+', '.join(str(p) for p in Path('/usr/local/lib').glob('python*/site-packages/pyarrow')),flush=True)
'''.replace('ROOT',repr(root)).replace('NAME',repr(name))
cmd=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','-o','ServerAliveInterval=5','-o','ServerAliveCountMax=1','ownsolid-cpu','python3 -u -c '+shlex.quote(remote)]
error=(base/'native-stream-error.log').open('wb')
process=subprocess.Popen(cmd,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=error,bufsize=0)
def line():
 result=bytearray();deadline=time.monotonic()+25
 while True:
  wait=deadline-time.monotonic()
  if wait<=0 or not select.select([process.stdout],[],[],wait)[0]:raise TimeoutError('No acknowledgement; retained partial input must be queried before resume')
  b=os.read(process.stdout.fileno(),1)
  if not b:raise RuntimeError('SSH ended: '+str(process.poll()))
  result+=b
  if b==b'\n':return result.decode().strip()
try:
 state=json.loads(line());offset=state['size'];assert offset<=len(data) and state['sha256']==hashlib.sha256(data[:offset]).hexdigest()
 print('Receiver verified prefix '+str(offset),flush=True)
 for start in range(offset,len(data),8192):
  end=min(start+8192,len(data));message={'offset':start,'prefix':hashlib.sha256(data[:start]).hexdigest(),'data':base64.b64encode(data[start:end]).decode()}
  process.stdin.write((json.dumps(message)+'\n').encode());process.stdin.flush()
  ack=line();assert ack==str(end)+' '+hashlib.sha256(data[:end]).hexdigest();print('Acknowledged '+str(end)+'/'+str(len(data)),flush=True)
 digest=hashlib.sha256(data).hexdigest();process.stdin.write((json.dumps({'done':True,'size':len(data),'sha256':digest})+'\n').encode());process.stdin.flush()
 assert line()=='DONE '+digest
 print('Bundle verified '+digest,flush=True);print(line(),flush=True);print(line(),flush=True)
 process.stdin.close();raise SystemExit(process.wait(timeout=10))
finally:
 if process.poll() is None:process.terminate();process.wait(timeout=10)
 error.close()
