import ast,hashlib,json,pathlib,shlex,time,types
r=pathlib.Path(__file__).parent;entry=r/'full_native_gate.prepared.py';previous=r.parent/'api-compat/full_native_gate.py'
tree=ast.parse(entry.read_text());old=ast.parse(previous.read_text())
functions=lambda t:{n.name:n for n in t.body if isinstance(n,(ast.FunctionDef,ast.ClassDef))}
new_functions=functions(tree);old_functions=functions(old)
unchanged=['budget','reap_group','owned_children','group_alive','signal_owned','stop','spawn','metadata','expired','interrupted']
assert all(ast.dump(new_functions[n])==ast.dump(old_functions[n]) for n in unchanged)
context={'shlex':shlex,'time':time,'started':time.monotonic(),'proof':{}}
names=['ResourceRefused','GateTimeout','resources','node_options','positive_cap','budget']
exec(compile(ast.Module(body=[new_functions[n] for n in names],type_ignores=[]),str(entry)+'#pure-policy-checks','exec'),context)
checks=[]
for value,wanted in [('',4096),('--max-old-space-size=512',512),('--max-old-space-size 2048',2048),('--max_old_space_size=3072',3072),('--max-old-space-size=4096',4096),('--max-old-space-size=8192',4096),('--max-old-space-size=1024 --max-old-space-size=3072',1024),('--no-warnings --max-old-space-size=2048',2048)]:
 result=context['node_options'](value);assert int(result.rsplit('=',1)[1])==wanted;assert result.startswith(value);checks.append('absolute-node-cap')
for value in ['--max-old-space-size','--max-old-space-size=','--max-old-space-size=0','--max-old-space-size=-1','--max-old-space-size=bad','--max-old-space-size-percentage=1','--max_heap_size=512']:
 try:context['node_options'](value);raise AssertionError('Ambiguous/invalid heap accepted')
 except ValueError:checks.append('invalid-or-conflicting-heap-refused')
for inherited,expected in [('1','1'),('2','2'),('8','2')]:
 assert context['positive_cap']({'GOMAXPROCS':inherited},'GOMAXPROCS',2)==expected;checks.append('cpu-cap')
class TextFile:
 def __init__(self,text):self.text=text
 def read_text(self):return self.text

def sample(free=6144*1024,available=12288*1024,disk=30*1024**3,io=5,refused=False):
 context['Path']=lambda name:TextFile('MemFree: '+str(free)+' kB\nMemAvailable: '+str(available)+' kB\n' if name=='/proc/meminfo' else 'full avg10='+str(io)+' avg60=0 avg300=0 total=0\n')
 context['os']=types.SimpleNamespace(statvfs=lambda _:types.SimpleNamespace(f_bavail=disk,f_frsize=1))
 try:context['resources']()
 except context['ResourceRefused']:
  assert refused;checks.append('strict-resource-refusal');return
 assert not refused;checks.append('strict-resource-pass')
sample();sample(free=6144*1024-1,refused=True);sample(available=12288*1024-1,refused=True);sample(disk=30*1024**3-1,refused=True);sample(io=5.01,refused=True);sample(free=2048*1024,available=8192*1024,refused=True)
context.update(deadline=time.monotonic()+1200,execution_deadline=time.monotonic()+1180)
assert 1179<context['budget']()<=1180 and 1191<context['budget'](cleanup=True)<=1192;checks.append('absolute-reserved-budget')
proof={'entry_sha256':hashlib.sha256(entry.read_bytes()).hexdigest(),'checks':len(checks),'case_kinds':sorted(set(checks)),'passed':True,'unchanged_process_cleanup_functions':unchanged,'scope':'Pure Python policy/helper checks with synthetic public resource data; no entry, lock, lint, build, browser or capacity test executed','new_full_gate_run':False}
(r/'prepared-policy-verification.json').write_text(json.dumps(proof,indent=2)+'\n');print(json.dumps(proof,indent=2))
