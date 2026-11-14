import subprocess,pathlib,json,uuid,hashlib
p=pathlib.Path(__file__).parent
img='golang@sha256:cfa27959837144bf4649802ba38e59daffd0dd2a7994aa83daaf113285b0f551'
script="""set -eu
mkdir /tmp/acquired
apt-get update -o Acquire::Retries=0 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30
cd /tmp/acquired
apt-get download libssl-dev=3.5.7-1~deb13u3
dpkg-deb -I libssl-dev_3.5.7-1~deb13u3_arm64.deb
cp -a /var/lib/apt/lists /tmp/acquired/indexes
"""
cid=subprocess.check_output(['docker','create','--name','iam103-acquire-'+uuid.uuid4().hex[:10],'--memory','256m','--cpus','1','--pids-limit','64',img,'sh','-c',script],text=True).strip()
try:
 r=subprocess.run(['docker','start','-a',cid],text=True,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=180)
 (p/'source/package-acquisition.log').write_text(r.stdout)
 state=json.loads(subprocess.check_output(['docker','inspect',cid],text=True))[0]['State']
 (p/'source/package-acquisition-state.json').write_text(json.dumps(state,indent=2)+'\n')
 assert state['ExitCode']==0,r.stdout
 subprocess.run(['docker','cp',cid+':/tmp/acquired',str(p/'source/acquired')],check=True,capture_output=True)
 files=[{'path':str(f.relative_to(p/'source/acquired')),'sha256':hashlib.sha256(f.read_bytes()).hexdigest(),'bytes':f.stat().st_size} for f in sorted((p/'source/acquired').rglob('*')) if f.is_file()]
 (p/'source/package-acquisition-inventory.json').write_text(json.dumps(files,indent=2)+'\n')
 print(json.dumps({'state':'acquired','files':len(files),'package':[f for f in files if f['path'].endswith('.deb')]}),flush=True)
finally: subprocess.run(['docker','rm','-f',cid],check=True,capture_output=True)
