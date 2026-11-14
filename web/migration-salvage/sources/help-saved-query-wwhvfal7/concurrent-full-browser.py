
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import json,os,signal,subprocess,sys
run=Path(sys.argv[1]);work=Path.cwd();jobs=[];streams=[]
if any(key.startswith(('OWNSOLID_LIVE_','OWNSOLID_GW120_')) for key in os.environ):raise SystemExit('A live fixture variable is present; no ordinary full browser suite started')
source=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
assert source=='44ab52293e516128ba2efc0edc2cb2053acc3462',source
try:
 for app in ['console','portal']:
  stream=(run/(app+'-browser.log')).open('w');streams.append(stream)
  env={**os.environ,'PLAYWRIGHT_JSON_OUTPUT_FILE':str(run/(app+'-report.json'))}
  command=['pnpm','exec','playwright','test','--workers=2','--global-timeout=1200000','--output',str(run/(app+'-results')),'--reporter=line,json']
  child=subprocess.Popen(command,cwd=work/'web/apps'/app,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
  jobs.append((app,child,command))
 (run/'browser-plan.json').write_text(json.dumps({'source':source,'appsConcurrent':True,'workersPerApp':2,'allCases':True,'testSelection':None,'commands':{app:command for app,child,command in jobs},'pids':{app:child.pid for app,child,command in jobs}},indent=2)+'\n')
 codes={app:child.wait(timeout=1230) for app,child,command in jobs}
 for app,code in codes.items():(run/(app+'-browser.exit')).write_text(str(code)+'\n')
 print(json.dumps({'browserExits':codes,'appsConcurrent':True,'allCases':True}),flush=True)
 if any(codes.values()):raise SystemExit(1)
finally:
 for app,child,command in jobs:
  if child.poll() is None:
   try:os.killpg(child.pid,signal.SIGTERM)
   except ProcessLookupError:pass
   try:child.wait(timeout=5)
   except subprocess.TimeoutExpired:
    try:os.killpg(child.pid,signal.SIGKILL)
    except ProcessLookupError:pass
    child.wait(timeout=3)
 for stream in streams:stream.close()
