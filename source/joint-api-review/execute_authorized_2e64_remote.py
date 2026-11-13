import datetime,hashlib,json,os,pathlib,subprocess,sys,time
os.umask(0o077)
r=pathlib.Path('/root/lane-rel-tools');d=r/'joint-api-review/delivery-2e64ab4c-c5e4f1cb';w=pathlib.Path('/root/lane-rel-identity-audit-contract');a=r/'api-compat'
source='2e64ab4c6c16bd84ae5c101b9de5444550760ff2';tree='3bdf79d7d76f096c989884190c32e4c08de20356';entry=a/'full_native_gate.py';manifest=d/'joint-full-gate-2e64ab4c-manifest.json';result_path=a/'joint-native-gate-2e64ab4c-proof.json';raw_log=a/'joint-native-gate-2e64ab4c.log';execution=d/'execution-state.json';started=time.monotonic();gate=None
receipt={'source':source,'tree':tree,'phase':'connected','execution_authorized':True,'attempt_consumed':True,'entry_started':False,'entry_executed':False,'entry_exit':None,'four_target_result':None,'queue_created':False,'retry':False}
frozen={'identity-audit-contract/go-native-linux-proof.json':'cd648aa45ce2ab13f08838b9254a18b2151ca93c56f0201a3f1f9ca1d5e13cd1','api-compat/joint-native-gate-9be01cbb-proof.json':'ea63d0f4d26a25744f322da5402381397c7fd14768c7afb8ab9b4c8509aaa5fe','api-compat/joint-native-gate-9be01cbb.log':'fbcc353ee78c27155e318b3c71fd4ec57fea98bf551080391dd094736c1044c3','api-compat/full_native_gate.executed-9be-1a5f519c.py':'1a5f519cbbc52dec3b7dd4c31b199486a14674e7310f92678765a8e9bd04a15c'}
def sha(path):
 digest=hashlib.sha256()
 with path.open('rb') as content:
  for chunk in iter(lambda:content.read(1024*1024),b''):digest.update(chunk)
 return digest.hexdigest()
def write_new(path,value):
 with path.open('x') as out:json.dump(value,out,indent=2);out.write('\n')
def update_execution(**values):
 state=json.loads(execution.read_text());state.update(values);temporary=d/'execution-state.authorized-2e64.new';temporary.write_text(json.dumps(state,indent=2)+'\n');os.replace(temporary,execution)
def git(*args):return subprocess.check_output(['git','-c','core.commitGraph=false',*args],cwd=w,text=True,timeout=5).strip()
print('AUTHORIZED_EXACT_ATTEMPT_CONNECTED',flush=True)
try:
 receipt['phase']='exact-preflight'
 marker=d/'authorized-2e64ab4c-once.json'
 write_new(marker,{'source':source,'tree':tree,'authorized_after_trn_source':'e639db90','preceding_release_result_sha256':'e83e8265b8d64f7d3c8a442da6d3f39f03cd9e6a5adb44ad8807b5d98a40d4f8','preceding_release_receipt_sha256':'646782f5b2b2d41c729a0d87844c96ead12db617ef011017b6d2cd0344627d30','preceding_release_is_not_current_admission_evidence':True,'attempt_consumed':True,'created_utc':datetime.datetime.now(datetime.timezone.utc).isoformat()})
 assert not entry.is_symlink() and sha(entry)=='f08c440fa6a53054ef08da78b7ca2e3a13fb5a5409edbc7d3e2f62348790fe9a','Exact entry SHA mismatch'
 assert sha(manifest)=='60db5ee075c6b24e06d16a0453e851d140ffa44b88b2207310f911901b73f47c','Exact manifest SHA mismatch'
 assert sha(d/'joint-source-2e64ab4c.bundle')=='c5e4f1cbb7812b841de739c1524e6f3700d1e3aadfd5ddc5a2852784cda74a52','Exact source bundle SHA mismatch'
 assert git('rev-parse','HEAD')==source and git('rev-parse','HEAD^{tree}')==tree,'Exact worktree source/tree mismatch'
 assert git('status','--porcelain')=='','Worktree must be clean'
 for name,wanted in frozen.items():assert sha(r/name)==wanted,'Frozen evidence changed'
 assert not os.path.lexists(result_path) and not os.path.lexists(raw_log),'Attempt already has evidence'
 state=json.loads(execution.read_text());assert state['source']==source and state['tree']==tree and not state['entry_executed'],'Execution state is not fresh'
 receipt['preflight_passed']=True;receipt['entry_sha256']=sha(entry);receipt['manifest_sha256']=sha(manifest)
 receipt['toolchain_environment_file_sha256']=sha(r/'env')
 receipt['asset_fingerprints']={}
 for label,path in [('go','/root/lane-rel-tools/go/bin/go'),('node','/root/lane-rel-tools/node/bin/node'),('cargo_native','/root/lane-rel-tools/rustup/toolchains/1.99.1-x86_64-unknown-linux-gnu/bin/cargo'),('rustc_native','/root/lane-rel-tools/rustup/toolchains/1.99.1-x86_64-unknown-linux-gnu/bin/rustc'),('chromium','/root/lane-rel-tools/playwright-browsers/chromium-1248/chrome-linux64/chrome'),('chromium_headless','/root/lane-rel-tools/playwright-browsers/chromium_headless_shell-1248/chrome-headless-shell-linux64/chrome-headless-shell')]:
  path=pathlib.Path(path);receipt['asset_fingerprints'][label]={'path':str(path),'sha256':sha(path)}
 update_execution(execution_authorized=True,attempt_consumed=True,entry_executed=False,authorization_source='Coordinator assigned this one exact full segment after reported TRN release',actual_result=str(result_path),once_marker=str(marker),state_scope='Current authorized attempt; actual live admission belongs only to the exact entry')
 receipt['phase']='entry-running'
 with raw_log.open('xb') as log:
  gate=subprocess.Popen(['bash','--noprofile','--norc','-euc','. /root/lane-rel-tools/env; exec python3 /root/lane-rel-tools/api-compat/full_native_gate.py'],stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT)
  gate_started=time.monotonic();receipt['entry_started']=True;receipt['entry_executed']=True;receipt['entry_pid']=gate.pid
  update_execution(entry_executed=True,entry_started_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),entry_pid=gate.pid)
  print('EXACT_ENTRY_STARTED',flush=True)
  while True:
   remaining=1205-(time.monotonic()-gate_started)
   if remaining<=0:raise TimeoutError('Exact entry did not end within its deadline observation bound')
   try:receipt['entry_exit']=gate.wait(timeout=min(30,remaining));break
   except subprocess.TimeoutExpired:
    progress={'elapsed_seconds':round(time.monotonic()-gate_started)}
    if result_path.exists():
     current=json.loads(result_path.read_text());progress.update({name:current.get(name) for name in ['phase','admitted','web-install_exit','check_exit','build_exit','test_exit']})
    print('EXACT_ENTRY_PROGRESS '+json.dumps(progress),flush=True)
 receipt['phase']='finished'
 if result_path.exists():
  proof=json.loads(result_path.read_text());receipt['four_target_result']={name:proof.get(name) for name in ['web-install_exit','check_exit','build_exit','test_exit']};receipt['gate_passed']=proof.get('passed',False);receipt['admitted']=proof.get('admitted',False);receipt['entry_proof_sha256']=sha(result_path)
  update_execution(entry_executed=True,entry_finished=True,entry_exit=receipt['entry_exit'],actual_result=str(result_path),actual_result_sha256=sha(result_path),full_gate_passed=proof.get('passed',False),admitted=proof.get('admitted',False),capacity_profile_measured=bool(proof.get('admitted')),actual_target_exits=receipt['four_target_result'],owned_process_groups_cleared=proof.get('all_owned_process_groups_cleared'),source_clean_after=proof.get('source_clean_after'))
 for name,wanted in frozen.items():assert sha(r/name)==wanted,'Frozen evidence changed during attempt'
 receipt['frozen_evidence_unchanged']=True
except BaseException as error:
 receipt['error_phase']=receipt['phase'];receipt['phase']='failed';receipt['error_type']=type(error).__name__;receipt['failure']=str(error) if not isinstance(error,subprocess.TimeoutExpired) else 'Bounded metadata command timed out'
 if gate is not None and gate.poll() is None:
  gate.terminate()
  try:gate.wait(timeout=3)
  except subprocess.TimeoutExpired:
   gate.kill();gate.wait(timeout=3)
finally:
 receipt['transport_observed_seconds']=round(time.monotonic()-started,3)
 receipt_file=d/'authorized-2e64ab4c-launch-result.json'
 if not receipt_file.exists():write_new(receipt_file,receipt)
 exported={}
 for path in [receipt_file,execution,result_path,raw_log]:
  if path.is_file():
   import base64
   exported[path.name]={'sha256':sha(path),'content':base64.b64encode(path.read_bytes()).decode()}
 print('PRIVATE_AUTHORIZED_RESULT_JSON '+json.dumps({'receipt':receipt,'files':exported}),flush=True)
