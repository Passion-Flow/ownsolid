from pathlib import Path
import json,shlex,subprocess
b=Path(__file__).resolve().parent;r=json.loads((b/'native-handoff-receiver.json').read_text())['root']
s=r'''
from pathlib import Path
import json,sys
r=Path(sys.argv[1]);out={}
for root in (r,r/'fleet-kpis'):
 if not root.exists():continue
 row={}
 for name in ('build-stage','prepare-driver-exit','build-exit','installed-stage','installed-driver-exit','installed-exit','installed-start','installed-finish'):
  p=root/name
  if p.is_file():row[name]=p.read_text().strip()
 lognames=('installed-driver.log','installed.log') if root.name=='fleet-kpis' else (() if row.get('installed-exit')=='0' else ('prepare-driver.log','prepare-launch-second.log','installed-driver.log','installed.log'))
 for name in lognames:
  p=root/name
  if p.is_file():row[name]=p.read_text()[-1200:]
 for name in ('build-preflight.json','installed-preflight.json'):
  p=root/name
  if p.is_file():row[name]=json.loads(p.read_text())
 out[root.name]=row
print(json.dumps(out))
'''
p=subprocess.run(['ssh','-o','ConnectTimeout=20','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -I -c '+shlex.quote(s)+' '+shlex.quote(r)],capture_output=True,text=True,timeout=40)
if p.returncode==0:
 (b/'compact-native-status.json').write_text(p.stdout);print(json.dumps(json.loads(p.stdout),indent=2))
else:print(p.stderr)
raise SystemExit(p.returncode)
