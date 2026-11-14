
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import subprocess,sys
root=Path(__file__).resolve().parent
wait=Path('/Users/passion./.local/state/ownsolid/web-scopes/download-discovery-6gj4rt_h/gw120-edit/wait-window-owner.py')
result=subprocess.run([sys.executable,str(wait)])
if result.returncode:raise SystemExit(result.returncode)
run=(root/'expiry-target-path').read_text().strip()
raise SystemExit(subprocess.run([sys.executable,str(root.parent/'mac-window.py'),'bash',str(root/'expiry-target.sh'),run]).returncode)
