from pathlib import Path
import hashlib,json,os,subprocess,time
base=Path(__file__).resolve().parent;out=base/'publication';repo=base.parent.parent/'lane-arc-gw120';os.chdir(repo);lock=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
qualified='a446ec580cf6dffc63df5d8269518f1ef8175d40';deadline=time.monotonic()+1200;last=None
(out/'publisher-pid').write_text(str(os.getpid())+'\n')
while True:
 try: lock.mkdir()
 except FileExistsError:
  try: owner=(lock/'owner').read_text().strip()
  except FileNotFoundError: owner='initializing'
  if owner!=last: print('Publication queued behind '+owner,flush=True);last=owner
 else:
  (lock/'owner').write_text(str(out)+'\n');break
 if time.monotonic()>deadline: raise SystemExit('Bounded publication queue expired; main was not changed')
 time.sleep(10)
def command(argv,log=None):
 if log:
  with (out/log).open('wb') as f:return subprocess.run(argv,stdout=f,stderr=subprocess.STDOUT).returncode
 return subprocess.check_output(argv,text=True).strip()
helper=Path('../lane-arc-diagnostics/audit58-resume.UeFZoBlF/git-network.py').resolve()
try:
 assert command(['git','branch','--show-current'])=='codex/gw120-schema59-integration'
 assert command(['git','status','--porcelain'])==''
 assert (out/'final-check-exit').read_text().strip()=='0'
 for attempt in range(3):
  if command(['python3',str(helper),'fetch'],f'fetch-{attempt}.log')!=0:raise RuntimeError('Fresh main fetch failed; no push')
  head=command(['git','rev-parse','HEAD']);main=command(['git','rev-parse','origin/main'])
  ancestor=subprocess.run(['git','merge-base','--is-ancestor',main,head]).returncode==0
  if not ancestor:
   if command(['git','rebase','origin/main'],f'rebase-{attempt}.log')!=0:raise RuntimeError('Rebase requires local resolution; no push')
  diff=command(['git','diff','--name-only',qualified,'HEAD']).splitlines()
  changes=[name for name in diff if not(name.startswith('docs/') and name.endswith('.md'))]
  if changes:
   (out/'new-code-inputs.json').write_text(json.dumps(changes,indent=2)+'\n')
   raise RuntimeError('Main includes new product inputs; full gates must rerun before publication')
  (out/'source-equivalence.json').write_text(json.dumps({'qualified_source':qualified,'publication_head':command(['git','rev-parse','HEAD']),'origin_main':main,'changed_paths':diff,'all_product_inputs_equal':True},indent=2)+'\n')
  if not ancestor:
   env=os.environ.copy();env['PATH']=str(base/'offline-tools')+':'+env['PATH'];env.update({'GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','GOFLAGS':'-p=1','CARGO_NET_OFFLINE':'true'});env['PYTHON']=command(['uv','python','find','3.12'])
   with (out/f'check-after-rebase-{attempt}.log').open('wb') as f:
    subprocess.run(['caffeinate','-i','make','check'],env=env,stdout=f,stderr=subprocess.STDOUT,check=True)
  assert command(['git','status','--porcelain'])==''
  if command(['python3',str(helper),'push'],f'push-{attempt}.log')==0:
   if command(['python3',str(helper),'fetch'],'post-push-fetch.log')!=0:raise RuntimeError('Push returned success; remote confirmation fetch failed')
   published=command(['git','rev-parse','HEAD']);remote=command(['git','rev-parse','origin/main'])
   assert subprocess.run(['git','merge-base','--is-ancestor',published,remote]).returncode==0
   (out/'published').write_text(published+'\n'+remote+'\n');print('Published schema 59 by fast-forward:',published,'remote:',remote,flush=True);break
 else:raise RuntimeError('Fast-forward push still contended after three bounded attempts')
finally:
 if (lock/'owner').exists() and (lock/'owner').read_text().strip()==str(out):
  (lock/'owner').unlink();lock.rmdir();(out/'lease-released').write_text('publication lock released\n')
