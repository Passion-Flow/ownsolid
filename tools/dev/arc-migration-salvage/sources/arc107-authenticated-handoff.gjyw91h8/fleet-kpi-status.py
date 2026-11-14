from pathlib import Path
import json,shlex,subprocess
b=Path(__file__).resolve().parent;r=json.loads((b/'native-handoff-receiver.json').read_text())['root']+'/fleet-kpis'
s="""from pathlib import Path
import json,sys
r=Path(sys.argv[1]);out={}
for n in ('installed-stage','installed-driver-exit','installed-exit','installed-start','installed-finish','installed-lease.log'):
 p=r/n
 if p.is_file():out[n]=p.read_text()[-1500:]
for n in ('installed-driver.log','installed-launch.log','installed.log'):
 p=r/n
 if p.is_file():out[n]=p.read_text()[-2500:]
print(json.dumps(out))
"""
p=subprocess.run(['ssh','-o','ConnectTimeout=30','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -I -c '+shlex.quote(s)+' '+shlex.quote(r)],capture_output=True,text=True,timeout=60)
if p.returncode==0:
 (b/'latest-fleet-kpi-status.json').write_text(p.stdout)
 for k,v in json.loads(p.stdout).items():print(k+':\n'+v)
else:print(p.stderr)
raise SystemExit(p.returncode)
