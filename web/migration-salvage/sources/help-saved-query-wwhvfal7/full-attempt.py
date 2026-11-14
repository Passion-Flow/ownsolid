
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import json,os,stat,subprocess,sys
root=Path(sys.argv[1]);path=Path('/private/tmp/ownsolid-workstation-full-gate.lock');info=path.stat(follow_symlinks=False)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.geteuid() and info.st_ino==50313339
assert not (root/'full-attempt.json').exists(),'No repeated acquisition in this attempt'
command=[sys.executable,'/Users/passion./.local/state/ownsolid/web-scopes/mac-window.py','bash',str(root/'run-full-once.sh'),str(root)]
(root/'full-attempt.json').write_text(json.dumps({'phase':'one nonblocking original-full-window attempt','command':command,'createsWaiter':False,'source':'44ab52293e516128ba2efc0edc2cb2053acc3462'},indent=2)+'\n')
try:
 with (root/'full-attempt.log').open('w') as out:result=subprocess.run(command,stdout=out,stderr=subprocess.STDOUT)
except OSError as error:
 (root/'full-attempt-error.json').write_text(json.dumps({'phase':'launcher-error','errno':error.errno,'error':str(error),'busy':False},indent=2)+'\n');raise
started=(root/'full-current-path').exists()
result_info={'exit':result.returncode,'fullChildStarted':started,'createsWaiter':False,'repeatAttempt':False}
if not started:result_info['raw']=(root/'full-attempt.log').read_text()
(root/'full-attempt-result.json').write_text(json.dumps(result_info,indent=2)+'\n')
print(json.dumps(result_info))
raise SystemExit(result.returncode)
