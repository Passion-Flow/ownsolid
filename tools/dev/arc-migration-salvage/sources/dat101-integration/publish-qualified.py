from pathlib import Path
import hashlib,json,os,subprocess,tempfile
base=Path(__file__).resolve().parent;repo=base.parent.parent/'lane-arc-dat101';os.chdir(repo)
qualified='f110757995e081544b836ec240474a700176571e';expected='8123466c5a64b6424047f126815ae63100383bc6';prior='f726e56b9d07bb162b45c57409f91ab74d691ba3'
proof=base/'full-repository.fgc_4tyz';assert (proof/'exit').read_text().strip()=='0' and (proof/'ok').read_text().strip()==qualified
hashes={'check.log':'197c06c775d1a4509d7d8089b0e1b39f6eb4e3801b2c7eaebeb53f852ceeae6f','build.log':'d73107de9eeaf089d8b5892cc7f4f2042f319f865d59646c3d250bdb62f78e1b','test.log':'43f0bad0f36ed3c60c4ff98767d3268a5d00ed2b7eaf9607e018f49d290cd62c'}
for name,digest in hashes.items():assert hashlib.sha256((proof/name).read_bytes()).hexdigest()==digest
run=Path(tempfile.mkdtemp(prefix='publication.',dir=str(base)))
def git(*a):return subprocess.check_output(['git',*a],text=True).strip()
def execute(command,name,budget):
 with (run/name).open('ab') as out:subprocess.run(command,stdout=out,stderr=subprocess.STDOUT,check=True,timeout=budget)
assert git('branch','--show-current')=='codex/dat101-schema60-integration' and git('status','--porcelain')==''
assert git('rev-parse','HEAD')==expected
helper=base.parent/'audit58-resume.UeFZoBlF/git-network.py'
def network(action):execute(['python3',str(helper),action],action+'.log',110)
network('fetch');main=git('rev-parse','origin/main')
if main!=prior:
 changed=git('diff','--name-only',prior,main).splitlines()
 assert all(x.endswith('.md') for x in changed),'New executable inputs require affected qualification'
 execute(['git','rebase','origin/main'],'rebase.log',60)
changed=git('diff','--name-only',qualified,'HEAD').splitlines()
assert all(x.endswith('.md') for x in changed),'Qualified executable or test inputs changed'
for raw in git('ls-tree','-r','--name-only',main,'--','services/control/migrations/postgresql').splitlines():
 assert (repo/raw).read_bytes()==subprocess.check_output(['git','show',main+':'+raw])
print('Checking only changed documentation; unchanged full gate input '+qualified+'; proof '+str(run),flush=True)
execute(['make','check-layout','check-roadmap'],'documentation-check.log',90)
execute(['git','diff','--check'],'diff-check.log',30)
assert git('status','--porcelain')==''
head=git('rev-parse','HEAD');subprocess.run(['git','merge-base','--is-ancestor',main,head],check=True)
(run/'admission.json').write_text(json.dumps({'source':head,'qualified_source':qualified,'main_before':main,'full_gate':str(proof),'executable_and_test_inputs_unchanged':True,'post_gate_changes':changed,'full_gate_hashes':hashes,'changed_documentation_checks':'check-layout/check-roadmap/diff-check passed; no compiler and no shared heavy lock needed','postgres_proof':json.loads((base/'reused-schema60-postgres.json').read_text()),'native_tagged_replay':'not run; source transfer interrupted before full receiver verification','production_routes':'not registered; coordinated original and normalized artifact inventory pending'},indent=2)+'\n')
print('Publishing formal schema60 '+head,flush=True)
network('push');network('fetch');remote=git('rev-parse','origin/main');subprocess.run(['git','merge-base','--is-ancestor',head,remote],check=True)
(run/'published').write_text(head+'\n'+remote+'\n');(base/'latest-publication').write_text(str(run)+'\n')
print('Formal schema60 fast-forward confirmed '+head+'; remote '+remote,flush=True)
