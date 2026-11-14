from pathlib import Path
import io,tarfile,gzip,subprocess,json,hashlib,uuid
root=Path.cwd();b=Path(__file__).resolve().parent;source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip();before='59c9c86a432c8623ec6b68595bf575920ff89056';patch=subprocess.check_output(['git','diff','--binary',before,source]);changes=[]
raw=subprocess.check_output(['git','diff','--name-status','-z','--no-renames',before,source]).decode().split('\0')
for pos in range(0,len(raw)-1,2):
 status,path=raw[pos:pos+2];assert status=='M';data=subprocess.check_output(['git','show',source+':'+path]);changes.append({'status':status,'path':path,'mode':subprocess.check_output(['git','ls-tree',source,'--',path],text=True).split()[0],'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})
assert {e['path'] for e in changes}=={'docs/research/agent-gateway-rebalance.md','tests/e2e/agent_gateway_lifecycle_test.go','tests/e2e/agent_gateway_observation_test.go'}
m={'source':source,'tree':subprocess.check_output(['git','rev-parse','HEAD^{tree}'],text=True).strip(),'base_source':before,'base_tree':'263c546bc6a3d4b6477cea5e57f91328bf352dbe','patch_sha256':hashlib.sha256(patch).hexdigest(),'tracked_paths':subprocess.check_output(['git','ls-tree','-r','--name-only',source],text=True).splitlines(),'changes':changes}
packet=io.BytesIO()
with tarfile.open(fileobj=packet,mode='w:gz') as t:
 for name,data in [('update.patch.gz',gzip.compress(patch,9,mtime=0)),('update.json.gz',gzip.compress(json.dumps(m,separators=(',',':')).encode(),9,mtime=0))]:
  info=tarfile.TarInfo(name);info.size=len(data);info.mode=0o600;t.addfile(info,io.BytesIO(data))
name='installed-source-149a9501.tar.gz';(b/name).write_bytes(packet.getvalue());info={'source':source,'tree':m['tree'],'base_source':before,'changes':len(changes),'paths':len(m['tracked_paths']),'bytes':len(packet.getvalue()),'sha256':hashlib.sha256(packet.getvalue()).hexdigest(),'root':'/root/lane-arc107-installed.149a9501.'+uuid.uuid4().hex[:8]};(b/'installed-busy-receiver.json').write_text(json.dumps(info,indent=2)+'\n');print(json.dumps(info),flush=True)
s=(b/'upload-installed-current.py').read_text().replace('installed-current-receiver.json','installed-busy-receiver.json').replace('installed-source-59c9c86a.tar.gz',name);(b/'upload-installed-busy.py').write_text(s)
s=(b/'installed-current-prepare.py').read_text().replace('installed-source-59c9c86a.tar.gz',name).replace('47684',str(info['bytes'])).replace('3582f505f2ec293043a5aefdffb37978459aaf24e12d783dafec76879aa1050f',info['sha256']).replace("assert manifest['source']=='59c9c86a432c8623ec6b68595bf575920ff89056'","assert manifest['source']=='"+source+"'")
s=s.replace("qualified=Path('/root/lane-arc107-installed.129573ae.a4371d31');old=qualified/'source'","qualified=Path('/root/lane-arc107-installed.59c9c86a.363f9c73');old=qualified/'source'")
s=s.replace("tarfile.open(qualified/'installed-source-129573ae.tar.gz')","tarfile.open('/root/lane-arc107-installed.129573ae.a4371d31/installed-source-129573ae.tar.gz')")
s=s.replace("assert second['source']==manifest['base_source'] and second['tree']==manifest['base_tree']\n overlay(second);verify(old)","overlay(second)\n with tarfile.open(qualified/'installed-source-59c9c86a.tar.gz') as t: third=json.loads(gzip.decompress(t.extractfile('update.json.gz').read()))\n assert third['source']==manifest['base_source'] and third['tree']==manifest['base_tree']\n overlay(third);verify(old)\n assert {e['path'] for e in manifest['changes']}=={'docs/research/agent-gateway-rebalance.md','tests/e2e/agent_gateway_lifecycle_test.go','tests/e2e/agent_gateway_observation_test.go'}")
s=s.replace("'scope':'exact-source native CGO-free control and six existing e2e binaries; no container or test'","'scope':'verified test-only delta; reuse unchanged qualified production binaries, compile current e2e, then pure race/vet in loopback-only namespace; no database/container'")
s=s.replace(" for module,name,args,package in builds:\n", " retained=json.loads((qualified/'binary-evidence.json').read_text())\n for module,name,args,package in builds:\n  if name!='e2e.test':\n   previous=qualified/'binaries'/name;assert hashlib.sha256(previous.read_bytes()).hexdigest()==retained[name]['sha256'];shutil.copy2(previous,binaries/name);print(name+' byte-identical qualified binary reused',flush=True);continue\n")
start=s.index(' # Reproduce the installed parser failure');end=s.index(" shutil.copyfile(source/'deploy/compose/compose.yaml'",start)
old=s[start:end];func=old[old.index(' checked_env='):old.index(" selected=")]
correct=''' # Production inputs are byte-identical; qualify the changed e2e source directly.
'''+func+''' selected='^(TestGatewayConnectionLogOrdering|TestGatewayObservedRetirementTiming|TestGatewayObservationAdmission)$'
 binary=root/'current-parser-race.test'
 run_check(['taskset','-c',cpus,go,'-C',str(source/'tests/e2e'),'test','-c','-race','-o',str(binary),'.'],'compile-parser-current.log',300)
 script='set -euo pipefail; ip link set lo up; cd "$3"; exec taskset -c "$1" "$2" -test.v -test.timeout=90s -test.run="$4"'
 run_check(['unshare','--net','bash','-c',script,'arc107-busy',cpus,str(binary),str(source/'tests/e2e'),selected],'parser-current.log',105)
 log=(root/'parser-current.log').read_text();assert '\\nPASS\\n' in log and '--- FAIL:' not in log and '--- SKIP:' not in log
 run_check(['taskset','-c',cpus,go,'-C',str(source/'tests/e2e'),'vet','.'],'parser-vet.log',300)
 (root/'production-binary-reuse.json').write_text(json.dumps({'qualified_source':manifest['base_source'],'source':manifest['source'],'production_paths_unchanged':True,'retained_hashes':retained},indent=2)+'\\n')
 print('Current native e2e race and vet passed; production binaries reused byte for byte',flush=True)
'''
s=s[:start]+correct+s[end:];(b/'installed-busy-prepare.py').write_text(s)
