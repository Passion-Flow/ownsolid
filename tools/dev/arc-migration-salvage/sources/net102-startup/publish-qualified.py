from pathlib import Path
import fcntl,hashlib,json,os,stat,subprocess,tempfile
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-net102';os.chdir(repo)
qualified='96eeef81b104932f60505375ff63a746bb04dabd';expected='2025a1c877b9d1165739c25da84ff534a9b83c41';prior='4f76102ca99328e0b9063eb05b0352e62501e154'
proof=base/'full-repository.o4qux0ic';assert (proof/'exit').read_text().strip()=='0' and (proof/'revision').read_text().splitlines()[0]==qualified
hashes={'check.log':'167f7bd61b5796562288ae13f1779f20f297294a569cd1b0b6388a06468ba027','build.log':'536e832e0a186fe4cbd8d9fc368d19932177bdbe23e3b016e5251b45c07e788b','test.log':'a1a6fd01f973439403f7df3f85ffc4cbddffc8637abbc94b8a3b0e1c88bbf3ab'}
for name,digest in hashes.items():assert hashlib.sha256((proof/name).read_bytes()).hexdigest()==digest
carrier=Path('/private/tmp/ownsolid-workstation-full-gate.lock');fd=os.open(carrier,os.O_RDWR|os.O_NOFOLLOW);info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:os.close(fd);raise SystemExit(75)
try:
 assert (info.st_dev,info.st_ino)==(carrier.lstat().st_dev,carrier.lstat().st_ino)
 run=Path(tempfile.mkdtemp(prefix='publication.',dir=str(base)))
 def git(*a):return subprocess.check_output(['git',*a],text=True).strip()
 assert git('branch','--show-current')=='codex/net102-startup-qualification' and git('status','--porcelain')==''
 assert git('rev-parse','HEAD')==expected
 helper=base.parent/'audit58-resume.UeFZoBlF/git-network.py'
 def network(action):
  with (run/(action+'.log')).open('ab') as out:subprocess.run(['python3',str(helper),action],stdout=out,stderr=subprocess.STDOUT,check=True,timeout=110)
 network('fetch');main=git('rev-parse','origin/main')
 if main!=prior:
  changed=git('diff','--name-only',prior,main).splitlines()
  assert all(x.endswith('.md') for x in changed),'Main contains new build inputs; preserve candidate and qualify affected changes before publication'
  with (run/'rebase.log').open('wb') as out:subprocess.run(['git','rebase','origin/main'],stdout=out,stderr=subprocess.STDOUT,check=True)
 changed=git('diff','--name-only',qualified,'HEAD').splitlines()
 assert all(x.endswith('.md') for x in changed),'Qualified product inputs changed'
 with (run/'documentation-check.log').open('wb') as out:subprocess.run(['make','check-layout','check-roadmap'],stdout=out,stderr=subprocess.STDOUT,check=True)
 assert git('status','--porcelain')==''
 head=git('rev-parse','HEAD');subprocess.run(['git','merge-base','--is-ancestor',main,head],check=True)
 (run/'admission.json').write_text(json.dumps({'source':head,'qualified_source':qualified,'main_before':main,'full_gate':str(proof),'default_build_inputs_unchanged':True,'post_gate_changes':changed,'full_gate_hashes':hashes,'installed_source_to_pin':'pending','full_gate_platform':'Darwin arm64','postgres_startup_platform':'Linux arm64 network-none on Darwin arm64'},indent=2)+'\n')
 print('Publishing NET-102 shared startup '+head+'; proof '+str(run),flush=True)
 network('push');network('fetch');remote=git('rev-parse','origin/main');subprocess.run(['git','merge-base','--is-ancestor',head,remote],check=True)
 (run/'published').write_text(head+'\n'+remote+'\n');(base/'latest-publication').write_text(str(run)+'\n')
 print('NET-102 fast-forward confirmed '+head+'; remote '+remote,flush=True)
finally:fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
