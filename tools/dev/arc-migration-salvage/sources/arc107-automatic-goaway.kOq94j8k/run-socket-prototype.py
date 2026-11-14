from pathlib import Path
import base64,shlex,subprocess,uuid
base=Path(__file__).resolve().parent;remote='/root/lane-arc-handoff/arc107-sockets-'+uuid.uuid4().hex[:10]+'.py';raw=(base/'socket-observation-prototype.py').read_bytes()
code="""from pathlib import Path
import base64,json,os,subprocess
mem={}
for line in Path('/proc/meminfo').read_text().splitlines():
 k,_,v=line.partition(':')
 if k in ('MemFree','MemAvailable'):mem[k]=int(v.split()[0])
psi=float(Path('/proc/pressure/io').read_text().split('full avg10=')[1].split()[0])
print(json.dumps({'preflight':mem,'full_io_psi_avg10':psi,'scope':'short socket namespace prototype; no compiler, container or database'}),flush=True)
if mem['MemFree']<2097152 or mem['MemAvailable']<8388608 or psi>5:raise SystemExit(75)
p=Path(%r);p.write_bytes(base64.b64decode(%r));p.chmod(0o600)
child=\"import resource,runpy,subprocess,sys;resource.setrlimit(resource.RLIMIT_AS,(100663296,100663296));resource.setrlimit(resource.RLIMIT_CPU,(2,2));subprocess.run(['ip','link','set','lo','up'],check=True);sys.argv=[%r,'--self-test'];runpy.run_path(%r,run_name='__main__')\"
print(subprocess.check_output(['uname','-sm'],text=True).strip(),flush=True)
r=subprocess.run(['timeout','10s','unshare','--net','python3','-c',child]);raise SystemExit(r.returncode)
"""%(remote,base64.b64encode(raw).decode(),remote,remote)
with (base/'socket-prototype-linux.log').open('wb') as out:
 r=subprocess.run(['ssh','-o','BatchMode=yes','-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT,timeout=45)
(base/'socket-prototype-linux-exit').write_text(str(r.returncode)+'\n');(base/'socket-prototype-remote').write_text(remote+'\n');print((base/'socket-prototype-linux.log').read_text());print('Prototype exit',r.returncode)
raise SystemExit(r.returncode)
