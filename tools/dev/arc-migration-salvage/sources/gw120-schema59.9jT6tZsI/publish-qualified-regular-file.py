from pathlib import Path
import fcntl,hashlib,json,os,stat,subprocess,tempfile
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-gw120';os.chdir(repo)
head='4f76102ca99328e0b9063eb05b0352e62501e154';predecessor='1280ea79c94f54b37f63ec33541fabfa6325901c'
proof=base/'final-admission.ikeb619a'
d=json.loads((proof/'source-equivalence.json').read_text())
assert d['head']==head and d['origin_main']==predecessor and d['new_check']=='passed' and d['default_product_inputs_unchanged']
assert (proof/'check-exit').read_text().strip()=='0'
assert (proof/'check.log').stat().st_size>0
run=Path(tempfile.mkdtemp(prefix='qualified-publication.',dir=str(base)))
carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW);info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:os.close(fd);raise SystemExit(75)
try:
 assert (info.st_dev,info.st_ino)==(carrier.lstat().st_dev,carrier.lstat().st_ino)
 def git(*a):return subprocess.check_output(['git',*a],text=True).strip()
 assert git('branch','--show-current')=='codex/gw120-schema59-integration'
 assert git('status','--porcelain')=='' and git('rev-parse','HEAD')==head
 helper=base.parent/'audit58-resume.UeFZoBlF/git-network.py'
 def network(action):
  with (run/(action+'.log')).open('ab') as out:subprocess.run(['python3',str(helper),action],stdout=out,stderr=subprocess.STDOUT,timeout=110,check=True)
 network('fetch')
 assert git('rev-parse','origin/main')==predecessor, 'Main changed after final qualification; no publication'
 assert git('rev-parse','HEAD')==head and git('status','--porcelain')==''
 (run/'input.json').write_text(json.dumps({'head':head,'main':predecessor,'final_check':str(proof),'final_check_sha256':hashlib.sha256((proof/'check.log').read_bytes()).hexdigest(),'retained_qualification':str(base/'publication/admission.json'),'DEL_priority_window':'original runner exited at its deadline; containers and forwarding stopped, no newer live window observed'},indent=2)+'\n')
 print('Publishing qualified schema-59 candidate '+head+'; log '+str(run),flush=True)
 network('push');network('fetch')
 remote=git('rev-parse','origin/main')
 subprocess.run(['git','merge-base','--is-ancestor',head,remote],check=True)
 (run/'published').write_text(head+'\n'+remote+'\n');(base/'latest-qualified-publication').write_text(str(run)+'\n')
 print('Fast-forward publication confirmed: '+head+'; remote '+remote,flush=True)
finally:fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
