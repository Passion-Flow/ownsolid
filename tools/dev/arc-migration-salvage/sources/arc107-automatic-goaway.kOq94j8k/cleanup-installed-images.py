from pathlib import Path
import fcntl,json,os,stat,subprocess,sys
roots=[Path('/root/lane-arc107-installed.129573ae.a4371d31'),Path('/root/lane-arc107-installed.129573ae.5f57c32b'),Path('/root/lane-arc107-installed.59c9c86a.363f9c73'),Path('/root/lane-arc107-installed.149a9501.c92cc7f9')]
locks=[]
try:
 for name in ['/run/lock/ownsolid-heavy.lock','/root/lane-sci-tools/heavy-test.lock']:
  p=Path(name);fd=os.open(p,os.O_RDWR|os.O_NOFOLLOW);s=os.fstat(fd);assert stat.S_ISREG(s.st_mode) and s.st_uid==os.getuid()
  try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
  except BlockingIOError:os.close(fd);raise SystemExit(75)
  assert (s.st_dev,s.st_ino)==(p.lstat().st_dev,p.lstat().st_ino);locks.append(fd)
 for root in roots:
  assert (root/'installed-exit').read_text().strip()=='1' and not list((root/'installation-scratch').iterdir())
  proof=json.loads((root/'fixture-image.json').read_text());tag=proof['tag'];assert tag.startswith('ownsolid/control:arc107-')
  before=subprocess.run(['docker','image','inspect',tag],text=True,capture_output=True)
  if before.returncode:
   assert 'No such image:' in before.stderr;print(tag+' already absent',flush=True);continue
  actual=json.loads(before.stdout)[0];assert actual['Id']==proof['id'];assert actual['Config']['Labels']['ownsolid.fixture']=='arc107-installed';assert actual['Config']['Labels']['org.opencontainers.image.revision']==proof['source']
  users=subprocess.check_output(['docker','ps','-a','--filter','ancestor='+actual['Id'],'--format','{{.ID}}'],text=True).strip();assert not users,'An image is still referenced by a container; leave it untouched'
  output=subprocess.check_output(['docker','image','rm',tag],text=True);(root/'image-cleanup.log').write_text(output);print('Removed owned fixture tag '+tag,flush=True)
finally:
 for fd in reversed(locks):fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
