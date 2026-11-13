"""Update only lane D's aggregate handoff after actual native startup proof."""
import datetime,hashlib,json,os,shlex,signal,subprocess
from pathlib import Path
root=Path(__file__).resolve().parent
code='''import hashlib,json,sys
from pathlib import Path
root=Path('/root/lane-rel-tools');p=root/'dev102-current-handoff.json';d=json.loads(p.read_text());v=json.loads(sys.argv[1]);a=root/'user-reads/native-client-410ed356';proof=json.loads((a/'native-startup.json').read_text())
assert proof['exit']==0 and proof['cases_passed']==11 and proof['source']==v['source']
assert hashlib.sha256((a/'ownsolid').read_bytes()).hexdigest()==v['sha256']==proof['binary_sha256']
for f,sha in v['frozen'].items():assert hashlib.sha256((root/f).read_bytes()).hexdigest()==sha
assert d['status_commit'] in ['f91c7ecbc046a5509dbc6cfb00f1679086993fff',v['status']]
d['legacy_control_source']='4463bbe628f60486bbdb92c15f72311fb53d076a';d['installed_control_source']=None;d['current_installed_acceptance']={'ready':False,'required_schema':59,'boundary':v['acceptance_boundary']}
d['status_commit']=v['status'];d['status_commit_scope']='docs/product/status.md and docs/research/cli.md only; runtime CLI remains exact 410ed356'
d['users'].update(v['users']);d['members']['complete_gate_and_installed_qualification']='Included in 410ed356 Darwin check/build and native Linux startup; installed qualification pending'
d['session']['full_linux_gate']='Not started; short native CLI build passed under all three locks. Whole-repository Linux gate remains separately queued.'
d['mac_combined_gate'].update(native_linux_startup_passed=True,native_linux_artifact=str(a/'ownsolid'))
d['mac_combined_gate']['receiving_state'].update(transfer_abandoned_for_verified_native_build=True,artifact_execution=False,reason='Partial transfer is unqualified; use separately admitted byte-identical native build.')
d['dispatch_boundaries']=['Frozen 8f/0d binaries and handoffs unchanged','Qualified native 410ed356 client is separate from partial reception','No new installation, account, shared session, schema or main publication','GW-104 activation and OBS-110 publication remain independent']
d['obs110_private_review']={'path':str(root/'obs110-review-3cebfcef'),'review_sha256':json.loads((root/'obs110-review-3cebfcef/review.json').read_text())['review_sha256'],'public_changes':False,'validation_status':400}
d['artifact_files_sha256']={n:hashlib.sha256((a/n).read_bytes()).hexdigest() for n in ['ownsolid','client-manifest.json','build-metadata.txt','native-startup.json','native-startup.stdout','native-startup.stderr','receipt.json']}
d['updated_utc']=v['utc'];p.write_text(json.dumps(d,indent=2)+'\\n')
print(json.dumps({'handoff_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'status_commit':d['status_commit'],'native_startup':proof,'artifact_files_sha256':d['artifact_files_sha256'],'obs110_private_review':d['obs110_private_review']}))'''
v={'source':'410ed356ffaf9c5a54a32d9b19e20ea457090f7b','sha256':'e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257','status':'79f218f9c19ae6808b54adffc249bc5fb2c694a3','utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'frozen':{'workspace-installed-8f4dd72c/client/ownsolid':'2f16b65de0753601aea7088c24afec774111cd85c7d027952309e8d231d0ca2d','session-installed-0d262a8a/client/ownsolid':'c6504aad053bd76a4c1fe159b49cfe3e778d526b7c33e6ca6a50f0143cdb943d','workspace-installed-8f4dd72c/handoff.json':'473b9a44c3b16531566259a918b3e87d578ee3103531465e04d825b6d663f4e8','session-installed-0d262a8a/handoff.json':'cca484799213ec290850822412ce7ca17000fd5f5577c3fc82a51f0d06b5fd0f'},'acceptance_boundary':'Await a newly qualified DEL original installation source and dedicated disposable accounts. Frozen session driver remains legacy-source-bound; adapt a separate package for schema59.','users':{'status_commit':'79f218f9c19ae6808b54adffc249bc5fb2c694a3','binary':'/root/lane-rel-tools/user-reads/native-client-410ed356/ownsolid','native_startup_proof':'/root/lane-rel-tools/user-reads/native-client-410ed356/native-startup.json','native_linux_startup_passed':True,'qualified_native_artifact_available':True,'receiving_file_admitted':False,'binary_build_scope':'Native offline Linux amd64 clean 410ed356; SHA identical to independent Darwin cross-build','qualification_boundary':'Native artifact and eleven startup checks passed. Real Admin/PostgreSQL authorization, exact-source whole Linux gate and whole make test remain unrun.','status_bundle':'/root/lane-rel-tools/user-reads-native-status.bundle','status_bundle_sha256':'5609cd0bde2b330dd744130171fcbf205c78f946b144794583ed564366fd52cb'}}
(root/'handoff-update-payload.json').write_text(json.dumps(v,indent=2)+'\n')
p=subprocess.Popen(['ssh','-T','-S','none','-o','BatchMode=yes','-o','ConnectTimeout=20','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=3','ownsolid-cpu',shlex.join(['python3','-c',code,json.dumps(v,separators=(',',':'))])],stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
try:out,err=p.communicate(timeout=55)
except subprocess.TimeoutExpired:
 os.killpg(p.pid,signal.SIGTERM);out,err=p.communicate(timeout=5)
record={'exit':p.returncode,'stdout':out.decode(),'stderr':err.decode()};(root/'handoff-publish-result.json').write_text(json.dumps(record,indent=2)+'\n');print(out.decode());print(err.decode());print('handoff_publish_exit',p.returncode)
