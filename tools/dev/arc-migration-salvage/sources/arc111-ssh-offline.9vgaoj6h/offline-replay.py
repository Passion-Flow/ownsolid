from pathlib import Path
import ast,contextlib,hashlib,io,json,socket,subprocess,types,unittest.mock as mock
out=Path(__file__).resolve().parent;base=out.parent;prior=base/'arc111-native-formal61.qtc543lk'
success=json.loads((out/'success-observation.json').read_text())
cases=[
 ('first-failure',(base/'arc111-native-e1.7axxa0p5/ssh.stdout').read_bytes(),(base/'arc111-native-e1.7axxa0p5/ssh.stderr').read_bytes(),255,255,'actual first-failure SSH bytes'),
 ('renewed-failure',(prior/'transport.stdout').read_bytes(),(prior/'transport.stderr').read_bytes(),255,255,'actual renewed-failure SSH bytes'),
 ('diagnostic-success-only',success['output']['text'].encode(),b'',0,1,'retained inspector observation format; not a native receipt or raw SSH transcript')]
results=[];network_calls=0
for label,stdout_bytes,stderr_bytes,status,new_expected,scope in cases:
 for version in ['original','corrected']:
  folder=out/'replay'/(version+'-'+label);folder.mkdir(mode=0o700,parents=True,exist_ok=True)
  for name in ['payload-manifest.json','binding.json','payload.tar']:
   target=folder/name
   if not target.exists():target.symlink_to(prior/name)
  target=folder/'remote-guard.py'
  if not target.exists():target.symlink_to(prior/'remote-guard.py' if version=='original' else out/'remote-guard.corrected-source-bytes')
  source=(out/('run-once.'+version+'-source-bytes')).read_text();calls=[]
  class ReplayProcess:
   def __init__(self,argv,**kwargs):
    assert argv[0] in ['ssh','/usr/bin/ssh'] and 'ownsolid-cpu' in argv
    assert kwargs['stdin']==subprocess.PIPE and kwargs['start_new_session'] and kwargs['close_fds']
    if version=='corrected':
     assert argv[0]=='/usr/bin/ssh' and '-vv' in argv and 'BatchMode=yes' in argv
     assert 'ConnectTimeout=10' in argv and 'ConnectionAttempts=1' in argv and '-n' not in argv
    self.pid=9000001;self.returncode=None;self.stdout_file=kwargs['stdout'];self.stderr_file=kwargs['stderr'];calls.append(argv[:])
   def communicate(self,data,timeout):
    assert type(data) is bytes and len(data)==12840960
    assert 0<timeout<= (1100 if version=='original' else 1080)
    self.stdout_file.write(stdout_bytes);self.stdout_file.flush();self.stderr_file.write(stderr_bytes);self.stderr_file.flush();self.returncode=status
    return None,None
  def replay_run(argv,**kwargs):
   assert argv[0]=='ps',argv
   # Pure recorder fixture. No process was spawned or inspected for this fake PID.
   text='9000001 0 9000001 REPLAY_ONLY\n' if len(argv)>4 and 'ppid=' in argv[-1] else ''
   return types.SimpleNamespace(stdout=text,stderr='',returncode=0 if text else 1)
  with mock.patch.object(subprocess,'Popen',ReplayProcess),mock.patch.object(subprocess,'run',replay_run),mock.patch.object(socket,'socket',side_effect=AssertionError('NETWORK_FORBIDDEN_IN_REPLAY')),mock.patch.object(socket,'create_connection',side_effect=AssertionError('NETWORK_FORBIDDEN_IN_REPLAY')):
   captured=io.StringIO();code=0
   try:
    with contextlib.redirect_stdout(captured):exec(compile(source,str(folder/'run-once.py'),'exec'),{'__file__':str(folder/'run-once.py'),'__name__':'__main__'})
   except SystemExit as error:code=error.code
  assert len(calls)==1
  expected=0 if version=='original' else new_expected
  assert code==expected,(label,version,code,expected)
  record=json.loads((folder/'outer-result.json').read_text())
  assert record['ssh_exit']==status and record['state'] in ['NOTRUN','INCOMPLETE_NO_REMOTE_RESULT']
  results.append({'case':label,'version':version,'scope':scope,'raw_stdout_sha256':hashlib.sha256(stdout_bytes).hexdigest(),'raw_stderr_sha256':hashlib.sha256(stderr_bytes).hexdigest(),'replayed_ssh_status':status,'outer_exit':code,'state':record['state'],'real_network_or_process_calls':0})
# Extract only pure policy functions: no launch code is executed here.
tree=ast.parse((out/'run-once.corrected-source-bytes').read_text());functions=ast.Module(body=[n for n in tree.body if isinstance(n,ast.FunctionDef)],type_ignores=[]);ns={};exec(compile(functions,'pure-offline-policy','exec'),ns)
assert ns['remaining_work_timeout'](19.858641375,1080,1100)<1080
assert ns['remaining_work_timeout'](1079.5,1080,1100)==.5
try:ns['remaining_work_timeout'](1080,1080,1100)
except TimeoutError:pass
else:raise AssertionError('expired work budget admitted')
assert ns['remaining_cleanup_timeout'](1199,1200,20)==1
assert ns['remaining_cleanup_timeout'](1201,1200,20)==0
for _,_,stderr,_,_,_ in cases[:2]:
 observation=ns['observed_ssh_stages'](stderr)
 assert observation['necessary_errors']==['Connection to UNKNOWN port 65535 timed out']
 assert all(v is None for v in observation['stages'].values())
assert ns['forwarded_outer_exit']({'outer_timeout':True,'ssh_exit':-15})==124
assert ns['forwarded_outer_exit']({'ssh_exit':0,'within_budget':True})==1
# New cap is stricter; all lock and TLS/host-key policies remain outside these changes.
remote=(out/'remote-guard.corrected-source-bytes').read_text()
assert "end=start+cfg['remote_budget_seconds']; work_end=end-120" in remote and "120<cfg['remote_budget_seconds']<=1000" in remote and 'end=min(end,cleanup_begin+120)' in remote
result={'state':'PASS','original_driver_sha256':hashlib.sha256((out/'run-once.original-source-bytes').read_bytes()).hexdigest(),'corrected_driver_sha256':hashlib.sha256((out/'run-once.corrected-source-bytes').read_bytes()).hexdigest(),'corrected_remote_guard_sha256':hashlib.sha256((out/'remote-guard.corrected-source-bytes').read_bytes()).hexdigest(),'driver_replays':len(results),'real_failed_transcript_replays':4,'unrelated_success_observation_replays':2,'corrected_unrelated_success_rejection':1,'pure_policy_checks':12,'real_network_connections':0,'real_child_processes':0,'go_node_rust_or_service_workloads':0,'product_files_changed':0,'candidate_activated':False,'cases':results,'qualification_scope':'Private wrapper outcome/deadline/observation logic only; not a transport fix, native SQL or gateway qualification.'}
(out/'offline-replay-result.json').write_text(json.dumps(result,indent=2,sort_keys=True)+'\n')
print(json.dumps({k:v for k,v in result.items() if k!='cases'},indent=2));print(json.dumps([{'case':x['case'],'version':x['version'],'outer_exit':x['outer_exit'],'ssh_exit':x['replayed_ssh_status']} for x in results],indent=2))
