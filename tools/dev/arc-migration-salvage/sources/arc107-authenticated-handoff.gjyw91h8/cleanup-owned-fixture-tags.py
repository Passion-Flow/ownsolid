from pathlib import Path
import json,shlex,subprocess
b=Path(__file__).resolve().parent
s=r'''
from pathlib import Path
import fcntl,json,os,stat,subprocess,time
root=Path('/root/lane-arc107-handoff.3ae6bfeb.ae445841');marker=root/'fixture-tag-cleanup.json';os.umask(0o077)
if marker.exists():print(marker.read_text());raise SystemExit(0)
assert (root/'installed-driver-exit').read_text().strip()=='0' and (root/'fleet-kpis/installed-driver-exit').read_text().strip()=='0'
locks=[]
try:
 for name in ('/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock'):
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);i=os.fstat(fd);assert stat.S_ISREG(i.st_mode) and i.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert(i.st_dev,i.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 rows=[]
 for directory,source,digest in (('/root/lane-arc107-handoff.821227ea.08099413','821227ea5b94a8357f695c290e78af3b81c54f22','sha256:0c57f136cefe12a877997a1477846ffe173912a715b3a8e0b42b6dcfdfde0b46'),(str(root),'3ae6bfebb67b47d684de4651cc597c26f104d725','sha256:704f35c660d6a90fb4bf4d8756289c1045deaabd0f31bb4246537e1468683f89')):
  p=Path(directory);assert not p.is_symlink() and p.stat().st_uid==os.getuid()
  item=json.loads((p/'fixture-image.json').read_text());tag=item['tag'];assert tag.startswith('ownsolid/control:arc107-handoff-') and item['id']==digest and item['source']==source
  data=json.loads(subprocess.check_output(['docker','image','inspect',tag],timeout=10))[0]
  assert data['Id']==digest and data['Config']['Labels']['org.opencontainers.image.revision']==source and data['Config']['Labels']['ownsolid.fixture']=='arc107-installed'
  references=subprocess.check_output(['docker','ps','-a','--filter','ancestor='+digest,'--format','{{.ID}}'],text=True,timeout=10).strip()
  if references:rows.append({'tag':tag,'id':digest,'removed':False,'reason':'A container still references this exact image'});continue
  output=subprocess.check_output(['docker','image','rm',tag],text=True,timeout=20)
  absent=subprocess.run(['docker','image','inspect',tag],capture_output=True,timeout=10);assert absent.returncode!=0
  rows.append({'tag':tag,'id':digest,'removed':True,'exact_owned_tag_only':True,'no_container_references':True,'docker_result':output.strip()})
 result={'at':time.time(),'rows':rows,'all_sources_raw_logs_and_shared_images_retained':True,'no_prune':True};assert not marker.exists();marker.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result))
finally:
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
'''
p=subprocess.run(['ssh','-o','ConnectTimeout=20','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -I -c '+shlex.quote(s)],capture_output=True,text=True,timeout=60)
print(p.stdout);print(p.stderr)
if p.returncode==0:(b/'owned-fixture-tag-cleanup.json').write_text(p.stdout)
raise SystemExit(p.returncode)
