from pathlib import Path
import base64,hashlib,json,shlex,subprocess,sys,uuid
base=Path(__file__).resolve().parent
metadata=base/'native-handoff-root.json'
if metadata.exists():root=json.loads(metadata.read_text())['root']
else:
 root='/root/lane-arc-dat101-native.f1107579.'+uuid.uuid4().hex[:8]
 metadata.write_text(json.dumps({'root':root,'source':'f110757995e081544b836ec240474a700176571e'})+'\n')
def remote(code):
 r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=8','-o','ServerAliveInterval=5','-o','ServerAliveCountMax=1','ownsolid-cpu','python3 -c '+shlex.quote(code)],capture_output=True,text=True,timeout=30)
 if r.returncode:raise RuntimeError('Remote operation refused '+str(r.returncode)+': '+r.stderr[-1500:])
 return r.stdout.strip()
print('Native input directory '+root,flush=True)
for filename in sys.argv[1:]:
 source=base/filename;data=source.read_bytes();digest=hashlib.sha256(data).hexdigest()
 state=json.loads(remote("from pathlib import Path; import hashlib,json,os,stat; root=Path("+repr(root)+"); root.mkdir(mode=0o700,exist_ok=True); assert root.lstat().st_uid==os.geteuid() and stat.S_ISDIR(root.lstat().st_mode) and not root.is_symlink(); p=root/"+repr(source.name)+"; assert not p.exists() or (p.is_file() and not p.is_symlink() and p.stat().st_uid==os.geteuid()); b=p.read_bytes() if p.exists() else b''; print(json.dumps({'size':len(b),'sha256':hashlib.sha256(b).hexdigest()}))"))
 offset=state['size'];assert offset<=len(data) and state['sha256']==hashlib.sha256(data[:offset]).hexdigest()
 for start in range(offset,len(data),16384):
  part=data[start:start+16384];end=start+len(part)
  code="from pathlib import Path; import base64,hashlib,os; p=Path("+repr(root)+")/"+repr(source.name)+"; raw=base64.b64decode("+repr(base64.b64encode(part).decode())+"); fd=os.open(p,os.O_RDWR|os.O_CREAT|os.O_NOFOLLOW,0o600); f=os.fdopen(fd,'r+b'); before=f.read(); assert len(before)=="+str(start)+" and hashlib.sha256(before).hexdigest()=="+repr(hashlib.sha256(data[:start]).hexdigest())+"; f.write(raw);f.flush();os.fsync(f.fileno());f.close(); print(str(p.stat().st_size)+' '+hashlib.sha256(p.read_bytes()).hexdigest())"
  ack=remote(code);assert ack==str(end)+' '+hashlib.sha256(data[:end]).hexdigest(),ack
  print(source.name+' acknowledged '+str(end)+'/'+str(len(data)),flush=True)
 final=remote("from pathlib import Path; import hashlib; p=Path("+repr(root)+")/"+repr(source.name)+"; assert p.stat().st_size=="+str(len(data))+" and hashlib.sha256(p.read_bytes()).hexdigest()=="+repr(digest)+"; print(hashlib.sha256(p.read_bytes()).hexdigest())")
 assert final==digest
 print(source.name+' verified '+digest,flush=True)
