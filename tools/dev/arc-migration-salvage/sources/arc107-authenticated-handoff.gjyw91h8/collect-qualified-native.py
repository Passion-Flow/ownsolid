from pathlib import Path
import base64,hashlib,json,shlex,subprocess,sys
b=Path(__file__).resolve().parent;parent=json.loads((b/'native-handoff-receiver.json').read_text())['root'];mode=sys.argv[1];assert mode in ('installed','fleet')
s=r'''
from pathlib import Path
import base64,hashlib,json,os,re,subprocess,sys,tarfile
inputs=Path(sys.argv[1]);mode=sys.argv[2];r=inputs/'fleet-kpis' if mode=='fleet' else inputs
assert (r/'installed-driver-exit').exists(),'Selected test has not completed'
exits={n:(r/n).read_text().strip() for n in ('installed-exit','installed-driver-exit')};assert all(v in ('0','1','124') for v in exits.values())
archive=inputs/'native-handoff-source.tar.gz';assert hashlib.sha256(archive.read_bytes()).hexdigest()=='c9bd5ffbbbbae19f08afd6c466762b5f7358616a78d6155a541e6c880a510e8a'
with tarfile.open(archive)as bundle:manifest=json.loads(bundle.extractfile('manifest.json').read())
source=inputs/'source';actual={str(p.relative_to(source)) for p in source.rglob('*') if p.is_file() or p.is_symlink()};assert actual==set(manifest['current'])
for name,item in manifest['current'].items():
 p=source/name
 if item['mode']=='120000':assert p.is_symlink();raw=os.readlink(p).encode()
 else:
  assert p.is_file() and not p.is_symlink() and bool(p.stat().st_mode&0o111)==(item['mode']=='100755');raw=p.read_bytes()
 assert hashlib.sha256(raw).hexdigest()==item['sha256'],name
names=['installed.log','installed-start','installed-finish','installed-preflight.json','fixture-image.json','runner-input.json','research-input.json','raw/egress.log','raw/routes-v4.log','raw/routes-v6.log','raw/interfaces.log','raw/routes-v4-last-read.json','raw/routes-v6-last-read.json','raw/outer-state.log']
if mode=='fleet':names+=['raw/database-observations.jsonl','raw/database-container-state.log']
else:
 names+=['binary-evidence.json','installed-source-verified.json','build-preflight.json','runtime-file-admission.log','cached-images.json']
 for n in ('client','sessiontransport','agentgateway','httpserver','agent-contract','simulator-fleet'):names.extend([n+'-race.log',n+'-vet.log'])
files={};total=0
for name in names:
 p=r/name;assert p.is_file() and not p.is_symlink(),name;raw=p.read_bytes();total+=len(raw);files[name]={'bytes':len(raw),'sha256':hashlib.sha256(raw).hexdigest(),'base64':base64.b64encode(raw).decode()}
assert total<4*1024**2
for name in ('routes-v4','routes-v6'):assert json.loads((r/'raw'/(name+'-last-read.json')).read_text())['code']==0
rawmanifest={}
for p in (r/'raw').iterdir():
 if p.is_file() and not p.is_symlink():
  data=p.read_bytes();rawmanifest[p.name]={'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()}
text=(r/'installed.log').read_text();outer=re.search(r'isolated Docker container (ownsolid-e2e-[a-z0-9-]+)',text).group(1)
remaining=subprocess.check_output(['docker','ps','-a','--filter','name=^/'+outer+'$','--format','{{.ID}}'],text=True);assert not remaining.strip()
assert not list((r/'installation-scratch').iterdir())
print(json.dumps({'exits':exits,'files':files,'raw_retained_remotely':rawmanifest,'source_after_run':{'source':manifest['source'],'tree':manifest['tree'],'paths':len(actual),'all_source_bytes_and_modes_unchanged':True},'cleanup':{'outer_container':outer,'outer_absent':True,'installation_scratch_empty':True,'fixture_image_retained_until_evidence_collected':True}}))
'''
p=subprocess.run(['ssh','-o','ConnectTimeout=25','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -I -c '+shlex.quote(s)+' '+shlex.quote(parent)+' '+shlex.quote(mode)],capture_output=True,text=True,timeout=60)
if p.returncode!=0:print(p.stdout);print(p.stderr);raise SystemExit(p.returncode)
result=json.loads(p.stdout);local=b/('fourth-native-installed-evidence' if mode=='installed' else 'fallback-fleet-kpi-evidence');assert not local.exists();local.mkdir(mode=0o700)
for name,item in result['files'].items():
 raw=base64.b64decode(item.pop('base64'));assert len(raw)==item['bytes'] and hashlib.sha256(raw).hexdigest()==item['sha256'];target=local/name;target.parent.mkdir(mode=0o700,parents=True,exist_ok=True);target.write_bytes(raw)
(local/'evidence-manifest.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'exits':result['exits'],'files':len(result['files']),'installed_log':result['files']['installed.log'],'source_after_run':result['source_after_run'],'cleanup':result['cleanup']}))
