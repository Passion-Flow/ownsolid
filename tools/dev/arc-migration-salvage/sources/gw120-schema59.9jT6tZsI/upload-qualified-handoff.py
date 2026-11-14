from pathlib import Path
import base64,hashlib,io,shlex,subprocess,tarfile,uuid
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-gw120'
files={'source.bundle':base/'gw120-12520508.bundle','handoff.json':base/'gw120-12520508-handoff.json','default-package-inputs.json':base/'default-package-inputs.json','linux-arm64-package-inputs.json':base/'linux-arm64-package-inputs.json','source-delta.json':base/'resume-product-delta.json','candidate.up.sql':repo/'services/control/migrations/postgresql/000059_model_alias_description.up.sql','candidate.down.sql':repo/'services/control/migrations/postgresql/000059_model_alias_description.down.sql','qualification.md':repo/'docs/research/gateway-model-description.md'}
buffer=io.BytesIO()
with tarfile.open(fileobj=buffer,mode='w:gz') as archive:
 for name,path in files.items():
  raw=path.read_bytes();entry=tarfile.TarInfo(name);entry.size=len(raw);entry.mode=0o600;archive.addfile(entry,io.BytesIO(raw))
raw=buffer.getvalue();assert len(raw)<95000
remote='/root/lane-arc-handoff/gw120-12520508.'+uuid.uuid4().hex[:10];digest=hashlib.sha256(raw).hexdigest()
(base/'qualified-handoff-remote').write_text(remote+'\n');(base/'qualified-handoff.tar.gz').write_bytes(raw)
code="""from pathlib import Path
import base64,hashlib,io,json,tarfile
p=Path(%r);p.parent.mkdir(mode=0o700,exist_ok=True);p.mkdir(mode=0o700)
raw=base64.b64decode(%r);assert hashlib.sha256(raw).hexdigest()==%r
(p/'handoff.tar.gz').write_bytes(raw)
with tarfile.open(fileobj=io.BytesIO(raw)) as archive:
 for entry in archive:
  assert entry.isfile() and '/' not in entry.name
  file=p/entry.name;file.write_bytes(archive.extractfile(entry).read());file.chmod(0o600)
manifest=json.loads((p/'handoff.json').read_text());assert hashlib.sha256((p/'source.bundle').read_bytes()).hexdigest()==manifest['bundle_sha256']
for direction in ('up','down'):
 assert hashlib.sha256((p/('candidate.'+direction+'.sql')).read_bytes()).hexdigest()==manifest[direction+'_sha256']
print('Verified frozen source '+manifest['source_commit']+' at '+str(p))
"""%(remote,base64.b64encode(raw).decode(),digest)
with (base/'qualified-handoff-upload.log').open('wb') as out:
 result=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=20','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT,timeout=70)
print((base/'qualified-handoff-upload.log').read_text());print('Archive',len(raw),'bytes SHA-256',digest)
raise SystemExit(result.returncode)
