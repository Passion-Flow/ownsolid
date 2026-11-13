"""Bind current qualified artifacts without changing frozen acceptance packages."""
import datetime,hashlib,json,os,signal,subprocess
from pathlib import Path
os.umask(0o077)
root=Path('/root/lane-rel-tools');artifact=root/'user-reads/native-client-410ed356';source='410ed356ffaf9c5a54a32d9b19e20ea457090f7b';status='79f218f9c19ae6808b54adffc249bc5fb2c694a3'
proof=json.loads((artifact/'native-startup.json').read_text());admission=json.loads((root/'user-reads/native-build-admission-410ed356.json').read_text())
assert proof['exit']==admission['exit']==0 and proof['cases_passed']==11 and proof['source']==admission['source']==source
sha=hashlib.sha256((artifact/'ownsolid').read_bytes()).hexdigest();assert sha==proof['binary_sha256']==admission['binary_sha256']=='e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
work=Path('/root/lane-rel-users');assert subprocess.check_output(['git','-C',str(work),'rev-parse','HEAD'],text=True).strip()==status
assert not subprocess.check_output(['git','-C',str(work),'status','--porcelain'],text=True)
assert not subprocess.check_output(['git','-C',str(work),'diff','--name-only',source,'--','tools/cli'],text=True)
manifest=json.loads((artifact/'client-manifest.json').read_text());manifest.update(native_linux_startup_run=True,native_startup_cases=11,cli_product_tree=subprocess.check_output(['git','-C',str(work),'rev-parse',source+':tools/cli'],text=True).strip(),pinned_inputs={name:hashlib.sha256((Path('/root/lane-rel-client-build-410ed356')/name).read_bytes()).hexdigest() for name in ['go.work','tools/cli/go.mod','tools/cli/go.sum']})
(artifact/'client-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
frozen={root/'workspace-installed-8f4dd72c/client/ownsolid':'2f16b65de0753601aea7088c24afec774111cd85c7d027952309e8d231d0ca2d',root/'session-installed-0d262a8a/client/ownsolid':'c6504aad053bd76a4c1fe159b49cfe3e778d526b7c33e6ca6a50f0143cdb943d',root/'workspace-installed-8f4dd72c/handoff.json':'473b9a44c3b16531566259a918b3e87d578ee3103531465e04d825b6d663f4e8',root/'session-installed-0d262a8a/handoff.json':'cca484799213ec290850822412ce7ca17000fd5f5577c3fc82a51f0d06b5fd0f'}
for path,digest in frozen.items():assert hashlib.sha256(path.read_bytes()).hexdigest()==digest,path
p=root/'dev102-current-handoff.json';handoff=json.loads(p.read_text());handoff['legacy_control_source']=handoff['installed_control_source'];handoff['installed_control_source']=None
handoff['current_installed_acceptance']={'ready':False,'required_schema':59,'boundary':'Await a newly qualified DEL original installation source and dedicated disposable accounts. Frozen session admission remains bound to the legacy source; adapt a new package without modifying the frozen one.'}
handoff['status_commit']=status;handoff['status_commit_scope']='docs/product/status.md and docs/research/cli.md only; runtime CLI subtree remains exact 410ed356'
handoff['users'].update(status_commit=status,binary=str(artifact/'ownsolid'),binary_sha256=sha,binary_build_scope='Native offline Linux amd64 build from clean 410ed356; byte-identical to independent Darwin cross-build',binary_manifest=str(artifact/'client-manifest.json'),native_startup_proof=str(artifact/'native-startup.json'),native_linux_startup_passed=True,receiving_file_admitted=False,qualified_native_artifact_available=True,qualification_boundary='Native offline artifact and 11 startup checks passed. Real Admin dispatcher/PostgreSQL account/workspace/member/session qualification, exact-source whole-repository Linux gate and whole make test remain unrun.',status_bundle=str(root/'user-reads-native-status.bundle'),status_bundle_sha256=hashlib.sha256((root/'user-reads-native-status.bundle').read_bytes()).hexdigest())
handoff['mac_combined_gate']['native_linux_startup_passed']=True
handoff['mac_combined_gate']['native_linux_artifact']=str(artifact/'ownsolid')
handoff['mac_combined_gate']['receiving_state'].update(transfer_abandoned_for_verified_native_build=True,artifact_execution=False,reason='Partial SSH reception remains unqualified. A separately admitted native offline build produced the exact expected ELF hash; use that artifact instead.')
handoff['members']['complete_gate_and_installed_qualification']='Included in exact 410ed356 Darwin check/build and native Linux startup; installed authorization remains pending'
handoff['session']['full_linux_gate']='Not started; the short native CLI build alone was admitted under all three heavy locks. Whole-repository Linux gate remains separately queued.'
handoff['dispatch_boundaries']=['Frozen 8f and 0d binaries and handoffs verified unchanged','Use the qualified native 410ed356 artifact for current client startup; partial transfer is not admitted','No new installation, service, container, account, shared session or schema created','GW-104 activation and OBS-110 publication remain independent','No new roadmap ticks or main publication']
handoff['obs110_private_review']={'path':str(root/'obs110-review-3cebfcef'),'public_changes':False,'review_sha256':json.loads((root/'obs110-review-3cebfcef/review.json').read_text())['review_sha256'],'validation_status':400,'mac_unit_evidence_verified':True,'linux_and_native_pending':True}
handoff['artifact_files_sha256']={name:hashlib.sha256((artifact/name).read_bytes()).hexdigest() for name in ['ownsolid','client-manifest.json','build-metadata.txt','native-startup.json','native-startup.stdout','native-startup.stderr','receipt.json']}
handoff['updated_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat();p.write_text(json.dumps(handoff,indent=2)+'\n')
for proc in Path('/proc').iterdir():
 if not proc.name.isdecimal() or int(proc.name)==os.getpid():continue
 try:
  cmd=(proc/'cmdline').read_bytes()
  if not cmd.startswith(b'python3\0-u\0-c\0') or b'Receive the source-bound lane D CLI with bounded, durable chunk ACKs.' not in cmd or b'verified-e630d9ff' not in cmd:continue
  fd=os.pidfd_open(int(proc.name));assert (proc/'cmdline').read_bytes()==cmd;signal.pidfd_send_signal(fd,signal.SIGTERM);os.close(fd)
 except (FileNotFoundError,ProcessLookupError):pass
print(json.dumps({'handoff':handoff,'handoff_sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'native_startup':proof,'native_admission':admission,'client_manifest':manifest}))
