import base64,hashlib,json,pathlib,subprocess,time
r=pathlib.Path('/Users/passion./.local/state/ownsolid/lane-rel');i=r/'identity-audit-contract'
expected=json.loads((i/'joint-absolute-attempt-previous-sha256.json').read_text())
names=list(expected)+['api-compat/check_entry_safety.py','api-compat/full-native-gate-absolute-deadline.diff','identity-audit-contract/joint-entry-absolute-deadline-review.json']
payload=[{'name':name,'expected':expected.get(name),'content':base64.b64encode((r/name).read_bytes()).decode(),'sha256':hashlib.sha256((r/name).read_bytes()).hexdigest()} for name in names]
remote="""import ast,base64,hashlib,json,os,pathlib,subprocess,time
r=pathlib.Path('/root/lane-rel-tools');w=pathlib.Path('/root/lane-rel-identity-audit-contract');a=r/'api-compat';started=time.monotonic();phase='connection';exit_code=1
print('CONNECTED_SINGLE_BOUNDED_ATTEMPT',flush=True)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def metadata(args):return subprocess.check_output(args,cwd=w,text=True,timeout=5).strip()
try:
 phase='source-and-artifact-verification'
 assert metadata(['git','rev-parse','HEAD'])=='9be01cbbf0144a2f2537667834539de31e40f58f'
 assert metadata(['git','rev-parse','HEAD^{tree}'])=='b8aea641ecd1b79be71270ad80ab03c1e131df31'
 assert metadata(['git','status','--porcelain'])==''
 assert sha(r/'identity-audit-contract/go-native-linux-proof.json')=='cd648aa45ce2ab13f08838b9254a18b2151ca93c56f0201a3f1f9ca1d5e13cd1'
 for suffix in ['.log','-proof.json','-entry-safety.json']:
  assert not os.path.lexists(a/('joint-native-gate-9be01cbb'+suffix))
 files=PAYLOAD
 for row in files:
  p=r/row['name'];content=base64.b64decode(row['content']);assert hashlib.sha256(content).hexdigest()==row['sha256']
  if row['expected'] is None:assert not os.path.lexists(p)
  else:assert p.is_file() and not p.is_symlink() and sha(p)==row['expected']
  if p.suffix=='.py':ast.parse(content)
 for row in files:
  p=r/row['name'];content=base64.b64decode(row['content'])
  if p.exists():
   archive=p.with_name(p.name+'.before-absolute-'+sha(p)[:12])
   if not archive.exists():
    with archive.open('xb') as f:f.write(p.read_bytes())
   temporary=p.with_name(p.name+'.absolute-new')
   with temporary.open('xb') as f:f.write(content)
   temporary.chmod(0o600);os.replace(temporary,p)
  else:
   with p.open('xb') as f:f.write(content)
   p.chmod(0o600)
 phase='entry-safety-functions'
 smoke=subprocess.run(['python3',str(a/'check_entry_safety.py')],stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,timeout=20)
 with (a/'joint-native-gate-9be01cbb-entry-safety.log').open('x') as f:f.write(smoke.stdout+smoke.stderr)
 if smoke.returncode:raise RuntimeError('Entry safety functions did not pass')
 print('ENTRY_SAFETY_PASSED',flush=True)
 phase='actual-single-entry'
 with (a/'joint-native-gate-9be01cbb.log').open('xb') as log:
  process=subprocess.Popen(['bash','--noprofile','--norc','-euc','. /root/lane-rel-tools/env; exec python3 /root/lane-rel-tools/api-compat/full_native_gate.py'],stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT)
  gate_started=time.monotonic();print('EXACT_GATE_STARTED',flush=True)
  while True:
   remaining=1210-(time.monotonic()-gate_started)
   if remaining<=0:
    process.terminate();raise TimeoutError('Entry exceeded its outer transport observation bound')
   try:exit_code=process.wait(timeout=min(30,remaining));break
   except subprocess.TimeoutExpired:
    print('EXACT_GATE_RUNNING_SECONDS',round(time.monotonic()-gate_started),flush=True)
 phase='finished'
except BaseException as error:
 print('ATTEMPT_PHASE_ERROR',phase,type(error).__name__,flush=True)
finally:
 result={'phase':phase,'exit':exit_code,'connection_elapsed_seconds':round(time.monotonic()-started,3),'no_queue_or_retry':True}
 exported={}
 for name in ['joint-native-gate-9be01cbb.log','joint-native-gate-9be01cbb-proof.json','joint-native-gate-9be01cbb-entry-safety.json','joint-native-gate-9be01cbb-entry-safety.log']:
  p=a/name
  if p.is_file():exported[name]={'content':base64.b64encode(p.read_bytes()).decode(),'sha256':sha(p)}
 result['artifacts']=exported
 print('PRIVATE_RESULT_JSON '+json.dumps(result),flush=True)
""".replace('PAYLOAD',repr(payload))
import ast
ast.parse(remote)
started=time.monotonic();capture=i/'joint-absolute-ssh-capture-syntax-corrected.log';error=i/'joint-absolute-ssh-stderr-syntax-corrected.log'
with capture.open('x') as out,error.open('x') as err:
 try:
  completed=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectionAttempts=1','-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -'],input=remote,text=True,stdout=out,stderr=err,timeout=1270)
  transport_exit=completed.returncode
 except subprocess.TimeoutExpired:transport_exit=124
lines=capture.read_text().splitlines();result=None
for line in lines:
 if line.startswith('PRIVATE_RESULT_JSON '):
  result=json.loads(line[len('PRIVATE_RESULT_JSON '):])
  for name,artifact in result.pop('artifacts').items():
   content=base64.b64decode(artifact['content']);assert hashlib.sha256(content).hexdigest()==artifact['sha256']
   with (r/'api-compat'/name).open('xb') as f:f.write(content)
 else:print(line)
summary={'transport_exit':transport_exit,'elapsed_seconds':round(time.monotonic()-started,3),'remote_result':result,'single_connection':True,'retry_or_background_queue':False,'stderr_sha256':hashlib.sha256(error.read_bytes()).hexdigest()}
if result is None:summary['transport_error_kind']='remote_syntax_error' if 'SyntaxError' in error.read_text() else 'ssh_transport_failed';summary['diagnostic_file']=str(error)
p=i/'joint-absolute-attempt-summary-syntax-corrected.json'
with p.open('x') as f:json.dump(summary,f,indent=2);f.write('\n')
print('BOUNDED_ATTEMPT_SUMMARY',json.dumps(summary))
proof=r/'api-compat/joint-native-gate-9be01cbb-proof.json'
if proof.exists():
 d=json.loads(proof.read_text());print('ACTUAL_RESULT',json.dumps({k:d.get(k) for k in ['exit','failure_kind','admitted','raw_memfree_mib','memavailable_mib','disk_free_bytes','io_full_avg10','web-install_exit','check_exit','build_exit','test_exit','elapsed_seconds','all_owned_process_groups_cleared','source_clean_after','locks']}))
