import ast,select,ctypes,hashlib,json,os,pathlib,shlex,signal,subprocess,sys,time
entry=pathlib.Path('/root/lane-rel-tools/api-compat/full_native_gate.py')
source=entry.read_text();tree=ast.parse(source)
names={'GateTimeout','budget','reap_group','group_alive','owned_children','signal_owned','stop','spawn','metadata','positive_cap','node_options','expired'}
parts=[node for node in tree.body if isinstance(node,(ast.FunctionDef,ast.ClassDef)) and node.name in names]
assert {node.name for node in parts}==names
context=dict(Path=pathlib.Path,os=os,ctypes=ctypes,signal=signal,subprocess=subprocess,sys=sys,time=time,shlex=shlex,w=pathlib.Path('/root/lane-rel-identity-audit-contract'),proof={'owned_process_groups':[]},child=None,group=None,finishing=False,deadline=time.monotonic()+30,execution_deadline=time.monotonic()+20)
exec(compile(ast.Module(body=parts,type_ignores=[]),str(entry)+'#actual-safety-functions','exec'),context)
def smoke_timeout(signum,frame):raise TimeoutError('Safety fixture deadline')
signal.signal(signal.SIGALRM,smoke_timeout);signal.setitimer(signal.ITIMER_REAL,12)
assert ctypes.CDLL(None,use_errno=True).prctl(36,1,0,0,0)==0
cases=[]
try:
 context['spawn']([sys.executable,'-c','pass'],'normal-exit',stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 assert context['child'].wait(timeout=2)==0
 context['stop']();assert context['group'] is None
 cases.append('normal leader exit leaves no owned group')
 read_fd,write_fd=os.pipe();os.set_blocking(read_fd,False)
 try:
  context['spawn']([sys.executable,'-c','import os,time\nif os.fork():os._exit(0)\ntime.sleep(60)'],'orphan-inherited-fd',stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,pass_fds=(write_fd,))
  os.close(write_fd);write_fd=None
  assert context['child'].wait(timeout=2)==0
  try:os.read(read_fd,1);raise AssertionError('Inherited pipe unexpectedly closed before cleanup')
  except BlockingIOError:pass
  context['stop']();assert context['group'] is None
  assert os.read(read_fd,1)==b''
  cases.append('exited leader orphan is reaped and inherited descriptor closes')
 finally:
  os.close(read_fd)
  if write_fd is not None:os.close(write_fd)
 read_fd,write_fd=os.pipe();os.set_blocking(read_fd,False)
 try:
  context['spawn']([sys.executable,'-c',f'import os,time\nif os.fork():os._exit(0)\nos.setsid()\nos.write({write_fd},b"R")\ntime.sleep(60)'],'detached-orphan-inherited-fd',stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,pass_fds=(write_fd,))
  os.close(write_fd);write_fd=None
  assert context['child'].wait(timeout=2)==0
  assert select.select([read_fd],[],[],2)[0] and os.read(read_fd,1)==b'R'
  context['stop']();assert context['group'] is None and not context['owned_children']()
  assert os.read(read_fd,1)==b''
  cases.append('detached owned orphan is reaped and inherited descriptor closes')
 finally:
  os.close(read_fd)
  if write_fd is not None:os.close(write_fd)
 context['deadline']=time.monotonic()+12;context['execution_deadline']=time.monotonic()+0.2
 previous_term=signal.signal(signal.SIGTERM,signal.SIG_IGN)
 try:
  context['metadata']([sys.executable,'-c','import time;time.sleep(60)'],'metadata-timeout')
  raise AssertionError('Metadata deadline did not fire')
 except context['GateTimeout']:pass
 finally:signal.signal(signal.SIGTERM,previous_term)
 assert context['group'] is None and context['proof']['owned_process_groups'][-1]['kill_sent']
 cases.append('metadata deadline terminates and reaps TERM-resistant group')
 context['execution_deadline']=time.monotonic()-1;before=len(context['proof']['owned_process_groups'])
 try:
  context['metadata']([sys.executable,'-c','pass'],'expired-before-spawn')
  raise AssertionError('Expired metadata budget admitted a new process')
 except context['GateTimeout']:pass
 assert context['child'] is None and len(context['proof']['owned_process_groups'])==before
 cases.append('expired remaining budget starts no metadata process')
 assert context['positive_cap']({'GOMAXPROCS':'1'},'GOMAXPROCS',2)=='1'
 assert context['positive_cap']({'GOMAXPROCS':'8'},'GOMAXPROCS',2)=='2'
 assert context['node_options']('--max-old-space-size=512').endswith('--max-old-space-size=512')
 assert context['node_options']('--max_old_space_size 256').endswith('--max-old-space-size=256')
 assert context['node_options']('--max-old-space-size=4096').endswith('--max-old-space-size=2048')
 cases.append('inherited stricter CPU and Node heap limits are retained')
 assert all(row['group_cleared'] for row in context['proof']['owned_process_groups'])
 result={'entry_sha256':hashlib.sha256(entry.read_bytes()).hexdigest(),'source':'9be01cbbf0144a2f2537667834539de31e40f58f','scope':'Actual entry safety functions only; synthetic subprocess fixtures, no make/Go/browser/shared lock, not a product or full-gate qualification','cases':cases,'groups':context['proof']['owned_process_groups'],'passed':True}
 out=entry.parent/'joint-native-gate-9be01cbb-entry-safety.json'
 with out.open('x') as f:json.dump(result,f,indent=2);f.write('\n')
 print('ENTRY_SAFETY_CASES_PASSED',len(cases),flush=True)
finally:
 if context['group'] is not None:context['stop']()
 signal.setitimer(signal.ITIMER_REAL,0)
