from pathlib import Path
import hashlib,json,os,signal,time
root=Path('/root/lane-arc111-tls-prototype.ee418b33')
allowed={str(root/'run-prototype.py').encode(),str(root/'queue.py').encode()}
def identity(p):
 try:
  cmd=(p/'cmdline').read_bytes();args=cmd.split(b'\0');exe=(p/'exe').resolve().name
  if not exe.startswith('python') or len(args)!=4 or args[1] not in allowed or args[2]!=str(root).encode():return None
  return {'pid':int(p.name),'start':(p/'stat').read_text().rsplit(') ',1)[1].split()[19],'sha256':hashlib.sha256(cmd).hexdigest()}
 except (OSError,ProcessLookupError):return None
stopped={}
for attempt in range(12):
 found=[v for p in Path('/proc').iterdir() if p.name.isdigit() and (v:=identity(p))]
 for v in found:
  p=Path('/proc')/str(v['pid'])
  if identity(p)==v:
   try:os.kill(v['pid'],signal.SIGSTOP);stopped[v['pid']]=v
   except ProcessLookupError:pass
 if all(v['pid'] in stopped for v in found):
  time.sleep(.2)
  newer=[v for p in Path('/proc').iterdir() if p.name.isdigit() and (v:=identity(p)) and v['pid'] not in stopped]
  if not newer:break
for v in stopped.values():
 if identity(Path('/proc')/str(v['pid']))==v:
  try:os.kill(v['pid'],signal.SIGKILL)
  except ProcessLookupError:pass
time.sleep(.5)
remaining=[v for p in Path('/proc').iterdir() if p.name.isdigit() and (v:=identity(p))]
result={'stopped_and_killed_exact_owned_processes':len(stopped),'identity_checks':list(stopped.values()),'remaining':remaining}
(root/'owned-recursion-cleanup.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({'stopped_and_killed_exact_owned_processes':len(stopped),'remaining':remaining,'runner_sha256':hashlib.sha256((root/'run-prototype.py').read_bytes()).hexdigest(),'runner_tail':(root/'run-prototype.py').read_text()[-1400:]}))
assert not remaining
