from pathlib import Path
import base64,hashlib,json,secrets,shlex,subprocess,sys,time
local=Path(__file__).resolve().parent
remote='/root/lane-arc111-tls-prototype.'+secrets.token_hex(4)
ssh=['ssh','-o','ConnectTimeout=30','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu']
def execute(code,timeout=60):
 return subprocess.check_output(ssh+['python3 -c '+shlex.quote(code)],text=True,timeout=timeout).strip()
execute('from pathlib import Path; import os; os.umask(0o077); p=Path('+repr(remote)+'); p.mkdir(mode=0o700); print(p)')
for name in ['prototype-input.tar.gz','run-prototype.py']:
 raw=(local/name).read_bytes();target=remote+'/'+name
 for offset in range(0,len(raw),4096):
  chunk=raw[offset:offset+4096]
  code='from pathlib import Path; import base64,hashlib; p=Path('+repr(target)+'); chunk=base64.b64decode('+repr(base64.b64encode(chunk).decode())+'); offset='+str(offset)+'; assert not p.exists() or p.stat().st_size>=offset; f=p.open("r+b" if p.exists() else "w+b"); f.seek(offset); f.write(chunk); f.close(); print(p.stat().st_size)'
  for attempt in range(3):
   try:execute(code);break
   except (subprocess.SubprocessError,OSError):
    if attempt==2:raise
    time.sleep(2)
 assert execute('from pathlib import Path; import hashlib; print(hashlib.sha256(Path('+repr(target)+').read_bytes()).hexdigest())')==hashlib.sha256(raw).hexdigest()
queue='''from pathlib import Path
import json,random,subprocess,sys,time
p=Path(sys.argv[1])
for attempt in range(1,121):
 with (p/'queue.log').open('ab') as log:
  code=subprocess.run(['python3',str(p/'run-prototype.py'),str(p)],stdout=log,stderr=subprocess.STDOUT).returncode
  log.write((json.dumps({'at':time.time(),'attempt':attempt,'exit':code})+'\\n').encode())
 if code!=75:break
 time.sleep(random.uniform(7,13))
(p/'queue-exit').write_text(str(code)+'\\n')
'''.replace("+'\\n'","+'\n'")
# Preserve escaped newlines in the remote Python source, rather than shell text.
queue="from pathlib import Path\nimport json,random,subprocess,sys,time\np=Path(sys.argv[1])\nfor attempt in range(1,121):\n with (p/'queue.log').open('ab') as log:\n  code=subprocess.run(['python3',str(p/'run-prototype.py'),str(p)],stdout=log,stderr=subprocess.STDOUT).returncode\n  log.write((json.dumps({'at':time.time(),'attempt':attempt,'exit':code})+'\\n').encode())\n if code!=75:break\n time.sleep(random.uniform(7,13))\n(p/'queue-exit').write_text(str(code)+'\\n')\n"
code='from pathlib import Path; import subprocess,os; p=Path('+repr(remote)+'); (p/"queue.py").write_text('+repr(queue)+'); log=(p/"launch.log").open("ab"); c=subprocess.Popen(["python3",str(p/"queue.py"),str(p)],stdin=subprocess.DEVNULL,stdout=log,stderr=log,start_new_session=True); print(c.pid)'
pid=int(execute(code))
metadata=json.loads((local/'input.json').read_text());metadata.update({'root':remote,'queue_pid':pid,'baseline_source':'149a95013db6c6d1ca9dbadd09b05fff9b4c98de'})
(local/'receiver.json').write_text(json.dumps(metadata,indent=2)+'\n')
print(json.dumps(metadata),flush=True)
