"""Transfer an already-built private client using bounded, checked SSH writes."""
import base64,gzip,hashlib,json,shlex,subprocess,time
from pathlib import Path
root=Path("/Users/passion./.local/state/ownsolid/lane-rel/gate-native-410ed356")
raw=(root/"client/ownsolid").read_bytes()
expected="e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257"
assert hashlib.sha256(raw).hexdigest()==expected
packed=gzip.compress(raw,compresslevel=6,mtime=0)
chunks=[packed[i:i+262144] for i in range(0,len(packed),262144)]
manifest={"source":"410ed356ffaf9c5a54a32d9b19e20ea457090f7b","sha256":expected,"size":len(raw),"gzip_sha256":hashlib.sha256(packed).hexdigest(),"gzip_size":len(packed),"chunks":[hashlib.sha256(x).hexdigest() for x in chunks],"client_manifest":json.loads((root/"client/client-manifest.json").read_text()),"build_metadata":(root/"client/build-metadata.txt").read_text()}
receiver=r"""import hashlib,os,sys,tempfile
from pathlib import Path
os.umask(0o077)
root=Path('/root/lane-rel-tools/user-reads/client-410ed356')
parts=root/'segments-e630d9ff'
parts.mkdir(mode=0o700,exist_ok=True)
index=int(sys.argv[1]);expected=sys.argv[2]
assert 0<=index<128 and len(expected)==64
data=sys.stdin.buffer.read(262145)
assert len(data)<=262144 and hashlib.sha256(data).hexdigest()==expected
target=parts/('part-%03d'%index)
if target.exists():
 assert not target.is_symlink() and hashlib.sha256(target.read_bytes()).hexdigest()==expected
else:
 fd,name=tempfile.mkstemp(prefix='incoming-',dir=parts)
 try:
  with os.fdopen(fd,'wb') as f:f.write(data);f.flush();os.fsync(f.fileno())
  os.replace(name,target)
 finally:
  if os.path.exists(name):os.unlink(name)
print('PART_OK',index)
"""
ssh=["ssh","-S","none","-o","ConnectTimeout=15","-o","ServerAliveInterval=30","-o","ServerAliveCountMax=6","ownsolid-cpu"]
attempts=[]
for index,data in enumerate(chunks):
 command=shlex.join(["python3","-c",receiver,str(index),manifest["chunks"][index]])
 for attempt in range(3):
  try:
   r=subprocess.run([*ssh,command],input=data,capture_output=True,timeout=55)
   ok=r.returncode==0 and r.stdout.strip()==("PART_OK "+str(index)).encode()
   code=r.returncode
  except subprocess.TimeoutExpired:ok=False;code="timeout"
  attempts.append({"part":index,"attempt":attempt+1,"exit":code})
  if ok:break
  time.sleep(1)
 else:
  (root/"chunk-transfer-attempts.json").write_text(json.dumps(attempts,indent=2)+"\n")
  raise SystemExit("Bounded transfer stopped; incomplete parts remain unqualified")
 print("CLIENT_PART_RECEIVED %d/%d"%(index+1,len(chunks)),flush=True)
assembler=r"""import gzip,hashlib,io,json,os,stat,sys,tempfile
from pathlib import Path
os.umask(0o077)
m=json.load(sys.stdin);root=Path('/root/lane-rel-tools/user-reads/client-410ed356')
assert m['source']=='410ed356ffaf9c5a54a32d9b19e20ea457090f7b'
assert m['sha256']=='e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
assert 0<m['size']<=32*1024*1024 and 0<len(m['chunks'])<128 and 0<m['gzip_size']<=32*1024*1024
parts=root/'segments-e630d9ff';blocks=[]
for i,expected in enumerate(m['chunks']):
 p=parts/('part-%03d'%i);assert stat.S_ISREG(p.lstat().st_mode)
 b=p.read_bytes();assert len(b)<=262144 and hashlib.sha256(b).hexdigest()==expected
 blocks.append(b)
packed=b''.join(blocks)
assert len(packed)==m['gzip_size'] and hashlib.sha256(packed).hexdigest()==m['gzip_sha256']
with gzip.GzipFile(fileobj=io.BytesIO(packed)) as stream:
 raw=stream.read(m['size']+1);assert len(raw)==m['size'] and not stream.read(1)
assert hashlib.sha256(raw).hexdigest()==m['sha256'] and raw[:4]==b'\x7fELF'
target=root/'ownsolid';assert stat.S_ISREG(target.lstat().st_mode)
fd,name=tempfile.mkstemp(prefix='verified-client-',dir=root)
try:
 os.fchmod(fd,0o700)
 with os.fdopen(fd,'wb') as f:f.write(raw);f.flush();os.fsync(f.fileno())
 os.replace(name,target)
finally:
 if os.path.exists(name):os.unlink(name)
assert m['client_manifest']['source_revision']==m['source'] and m['client_manifest']['binary_sha256']==m['sha256']
(root/'client-manifest.json').write_text(json.dumps(m['client_manifest'],indent=2)+'\n')
(root/'build-metadata.txt').write_text(m['build_metadata'])
(root/'received-client.json').write_text(json.dumps({'source':m['source'],'sha256':m['sha256'],'received_and_verified':True,'native_startup_run':False,'installed_acceptance':False},indent=2)+'\n')
print('CLIENT_RECEIVED_AND_HASH_VERIFIED')
"""
r=subprocess.run([*ssh,shlex.join(["python3","-c",assembler])],input=json.dumps(manifest).encode(),capture_output=True,timeout=55)
(root/"chunk-transfer-attempts.json").write_text(json.dumps(attempts,indent=2)+"\n")
if r.returncode!=0 or r.stdout.strip()!=b"CLIENT_RECEIVED_AND_HASH_VERIFIED":raise SystemExit("Assembly refused; native startup remains unrun")
print(r.stdout.decode().strip(),flush=True)
