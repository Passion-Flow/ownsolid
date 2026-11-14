
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
import os,sys,json,fcntl,stat,subprocess,signal,time,hashlib
from pathlib import Path
packet=Path(__file__).parent
root=Path(sys.argv[1]); web=root/'web'
fd=os.open('/private/tmp/ownsolid-workstation-full-gate.lock',os.O_RDWR|os.O_NOFOLLOW)
info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.geteuid() and info.st_ino==50313339
result={'scope':'focused formatter, Help generation, lint, types and unit tests only; no browser/build/full/native/installed','steps':[],'busy':False,'childStarted':False,'carrierInode':info.st_ino}
def save(): (packet/'result.json').write_text(json.dumps(result,indent=2)+'\n')
try:
 try: fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
 except BlockingIOError:
  result.update(busy=True,exitCode=75); save(); print('BUSY: zero work children; no retry'); sys.exit(75)
 disk=os.statvfs(root); assert disk.f_bavail*disk.f_frsize>=15*1024**3
 vm=subprocess.run(['vm_stat'],capture_output=True,text=True,timeout=5,check=True).stdout
 (packet/'vm-stat.txt').write_text(vm)
 import re
 page=int(re.search(r'page size of (\d+) bytes',vm).group(1)); free=int(re.search(r'Pages free:\s+(\d+)',vm).group(1))*page
 assert free>=2*1024**3, 'Less than 2 GiB free memory'
 env=dict(os.environ,NODE_OPTIONS='--max-old-space-size=512')
 changed=subprocess.run(['git','status','--porcelain','--untracked-files=all'],cwd=root,text=True,capture_output=True,check=True,timeout=5).stdout
 files=[line[3:] for line in changed.splitlines() if line[3:].startswith('web/') and '/gen/' not in line and line.endswith(('.ts','.tsx','.json','.md'))]
 input_files={name:hashlib.sha256((root/name).read_bytes()).hexdigest() for name in files}
 (packet/'input-files.json').write_text(json.dumps(input_files,indent=2)+'\n')
 steps=[('format',['pnpm','exec','prettier','--write',*[str(root/name) for name in files]],web),('help-generation',['node','tools/codegen/web-help.mjs'],root),('lint',['pnpm','exec','eslint','--config','packages/config/eslint.config.js','--max-warnings','0',*[str(root/name) for name in files if name.endswith(('.ts','.tsx'))]],web),('api-types',['pnpm','exec','tsc','--noEmit'],web/'packages/api'),('shell-types',['pnpm','exec','tsc','--noEmit'],web/'packages/shell'),('api-units',['pnpm','exec','vitest','run','src/browser-sessions.test.ts','src/client.test.ts','src/transport.test.ts','--maxWorkers=1'],web/'packages/api'),('session-units',['pnpm','exec','vitest','run','src/use-account-sessions.test.tsx','src/account-sessions.test.tsx','src/session.test.ts','--maxWorkers=1'],web/'packages/shell'),('confirmation-units',['pnpm','exec','vitest','run','src/components/components.test.tsx','--maxWorkers=1'],web/'packages/ui'),('locale-units',['pnpm','exec','vitest','run','--maxWorkers=1'],web/'packages/i18n'),('help-units',['pnpm','exec','vitest','run','src/help.test.ts','--maxWorkers=1'],web/'packages/help')]
 deadline=time.monotonic()+300
 for name,cmd,cwd in steps:
  assert time.monotonic()<deadline
  with (packet/(name+'.log')).open('w') as log:
   child=subprocess.Popen(cmd,cwd=cwd,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
   result['childStarted']=True
   try: code=child.wait(timeout=min(45,max(0.1,deadline-time.monotonic())))
   except subprocess.TimeoutExpired:
    os.killpg(child.pid,signal.SIGTERM)
    try: child.wait(timeout=1)
    except subprocess.TimeoutExpired: os.killpg(child.pid,signal.SIGKILL); child.wait(timeout=2)
    code=124
  result['steps'].append({'name':name,'exitCode':code}); save(); print(name+': '+str(code),flush=True)
  if code: print('Continuing independent focused checks',flush=True)
 result['exitCode']=int(any(step['exitCode'] for step in result['steps'])); save(); sys.exit(result['exitCode'])
finally:
 os.close(fd)
