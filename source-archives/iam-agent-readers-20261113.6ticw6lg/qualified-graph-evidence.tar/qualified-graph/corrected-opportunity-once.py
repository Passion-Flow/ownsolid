import hashlib,json,os,pathlib,re,signal,stat,subprocess,sys,tarfile,tempfile,time
started=time.monotonic();deadline=started+300
p=pathlib.Path(__file__).parent;repo=pathlib.Path((p/'repository-path').read_text().strip())
attempt=pathlib.Path(tempfile.mkdtemp(prefix='corrected-opportunity-',dir=p));(p/'active-corrected-opportunity').write_text(str(attempt)+'\n')
shas={'bundle':'e475f54c97137d11b7a9f40b687d809f69e4c65d0068822cd1d707858fd8feaf','manifest':'346e20c56a8032fba2815d4b06522c98ed9d96d7c814d9711f7161061757f6f5','patch':'8907bbf2f38ce3aec7effb424d33aa4f40bff795ac72fd638d14d1208e0fb53d'}
result={'budget_seconds':300,'cleanup_reserve_seconds':30,'attempt':str(attempt),'database_started':False,'retry_count':0}
def git(*args):return subprocess.check_output(['git',*args],cwd=repo,timeout=min(10,max(1,deadline-time.monotonic()-30)))
lock=pathlib.Path('/private/tmp/ownsolid-workstation-full-gate.lock');before=lock.lstat()
assert stat.S_ISREG(before.st_mode) and before.st_uid==os.geteuid() and before.st_ino==50313339 and before.st_size==0
result['carrier_before']={'device':before.st_dev,'inode':before.st_ino,'uid':before.st_uid,'size':before.st_size}
assert hashlib.sha256((p/'review-source-v1.tar').read_bytes()).hexdigest()==shas['bundle']
assert hashlib.sha256((p/'review-source-v1-hashes.json').read_bytes()).hexdigest()==shas['manifest']
manifest=json.loads((p/'review-source-v1-hashes.json').read_text());payload={}
with tarfile.open(p/'review-source-v1.tar') as tar:
 for member in tar.getmembers():
  assert member.isfile() and member.name.startswith('review-source-v1/')
  name=member.name[len('review-source-v1/'):];assert name in manifest and name not in payload
  raw=tar.extractfile(member).read();assert hashlib.sha256(raw).hexdigest()==manifest[name],name
  payload[name]=raw
assert set(payload)==set(manifest)
assert hashlib.sha256(payload['identity-graph.patch']).hexdigest()==shas['patch']
owned=json.loads((p/'owned-paths.json').read_text());assert len(owned)==4
assert not git('status','--porcelain').strip(),'WORKTREE_NOT_CLEAN'
result['input_base']=git('rev-parse','HEAD').decode().strip()
for name in owned:
 assert git('show','1af0ca5497fbbe83875eb6af4769fad4198135ac:'+name)==payload['owner/'+name]
producer=json.loads((p/'original-owner-hashes.json').read_text());assert len(producer)==21
for name,digest in producer.items():
 raw=git('show','e639db9035b0b2c0229c90175493c605f328e295:'+name)
 assert hashlib.sha256(raw).hexdigest()==digest and raw==payload['original-owner/'+name] and raw==(p/'original-owner'/name).read_bytes(),name
 if name.endswith('.go') and not name.endswith('_test.go'):
  assert raw==git('show','5b73fa05305c2c5d8178a256683ede8b4e9ef99f:'+name),name
replace=json.loads((p/'overlay.json').read_text())['Replace']
assert set(replace)=={str(repo/name) for name in producer if name.endswith('.go')}
assert all(pathlib.Path(target)==p/'original-owner'/str(pathlib.Path(source).relative_to(repo)) for source,target in replace.items())
branch='codex/iam-agent-readers-20261113'
assert subprocess.run(['git','show-ref','--verify','--quiet','refs/heads/'+branch],cwd=repo).returncode==1
subprocess.run(['git','switch','-c',branch],cwd=repo,check=True,timeout=10)
for name in owned:
 target=repo/name;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(payload['owner/'+name])
result['branch']=branch;result['source_hashes']={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in owned}
result['producer_hashes']=producer
(attempt/'verified-input.json').write_text(json.dumps(result,indent=2)+'\n')
helper=p/'check-graph-unit.py'
assert hashlib.sha256(helper.read_bytes()).hexdigest()==manifest['check-graph-unit.py']
command=['/usr/bin/python3','/Users/passion./.codex/ownsolid-evidence/iam102-20261113.1jjfl_s4/lock-run.py','/usr/bin/python3',str(helper)]
child=None;timed_out=False
try:
 with (attempt/'outer.log').open('xb') as output:
  child=subprocess.Popen(command,cwd=repo,stdout=output,stderr=subprocess.STDOUT)
  try:code=child.wait(timeout=max(1,deadline-time.monotonic()-30))
  except subprocess.TimeoutExpired:
   timed_out=True;child.send_signal(signal.SIGTERM)
   try:code=child.wait(timeout=min(25,max(1,deadline-time.monotonic()-5)))
   except subprocess.TimeoutExpired:child.kill();code=child.wait(timeout=3)
 result['return_code']=code;result['timed_out']=timed_out
finally:
 if child is not None and child.poll() is None:
  child.send_signal(signal.SIGTERM)
  try:child.wait(timeout=min(25,max(1,deadline-time.monotonic()-3)))
  except subprocess.TimeoutExpired:child.kill();child.wait(timeout=2)
 after=lock.lstat();assert (after.st_dev,after.st_ino,after.st_uid,after.st_size)==(before.st_dev,before.st_ino,before.st_uid,0)
 result['carrier_after']={'device':after.st_dev,'inode':after.st_ino,'uid':after.st_uid,'size':after.st_size}
 state=json.loads(pathlib.Path('/Users/passion./.codex/ownsolid-evidence/iam102-20261113.1jjfl_s4/lock-process.json').read_text())
 if child is not None and state.get('wrapper_pid')==child.pid:
  result['guard_state']=state
  pgid=state['child_pid'];rows=subprocess.check_output(['ps','-axo','pid,ppid,pgid,command'],text=True,timeout=3).splitlines()[1:]
  remaining=[row for row in rows if len(row.split(None,3))>=4 and row.split(None,3)[2]==str(pgid)]
  if remaining:
   os.killpg(pgid,signal.SIGTERM)
   time.sleep(.2)
   rows=subprocess.check_output(['ps','-axo','pid,ppid,pgid,command'],text=True,timeout=3).splitlines()[1:]
   remaining=[row for row in rows if len(row.split(None,3))>=4 and row.split(None,3)[2]==str(pgid)]
   if remaining:os.killpg(pgid,signal.SIGKILL)
  result['remaining_owned_group_after_exit']=remaining
  assert not remaining,'OWNED_GROUP_REMAINED'
 result['elapsed_seconds']=time.monotonic()-started
 (attempt/'result.json').write_text(json.dumps(result,indent=2)+'\n')
assert result['elapsed_seconds']<=300
if code==0:
 check=pathlib.Path((p/'latest-check').read_text().strip());unit=json.loads((check/'result.json').read_text())
 expected_roots={'TestAgentGatewayPublishedReaderGraph','TestAgentGatewayPublishedReaderFailureCleanup','TestAgentGatewayPublishedReaderRequiresTrustedOwner'}
 expected_subs={'TestAgentGatewayPublishedReaderFailureCleanup/'+name for name in ['foreign_tenant','missing_second_namespace','invalid_graph']}
 assert set(unit['passed_tests'])==expected_roots|expected_subs
 assert result['source_hashes']=={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in owned}
 result['unit_check']=str(check);result['passed_roots']=3;result['passed_subtests']=3;result['skip_count']=0;result['failure_count']=0
 result['qualified_scope']='exact four-path IAM graph plus original twenty-one-path producer overlay; no database/runtime/full gate'
 (attempt/'result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k not in ['source_hashes','producer_hashes']},indent=2),flush=True)
raise SystemExit(code)
