from pathlib import Path
import fcntl,hashlib,json,os,re,signal,stat,subprocess,sys,tempfile
base=Path(__file__).resolve().parent
repo=base.parent.parent/'lane-arc-gw120'
os.chdir(repo)
qualified='a446ec580cf6dffc63df5d8269518f1ef8175d40'
expected='4f76102ca99328e0b9063eb05b0352e62501e154'
expected_main='1280ea79c94f54b37f63ec33541fabfa6325901c'
publish=sys.argv[1:]==['--publish']
assert sys.argv[1:] in ([],['--publish'])
run=Path(tempfile.mkdtemp(prefix='final-admission.',dir=str(base)))
gate=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
fd=os.open(gate,os.O_RDWR|os.O_NOFOLLOW)
info=os.fstat(fd)
assert stat.S_ISREG(info.st_mode) and info.st_uid==os.getuid()
try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
except BlockingIOError:
 os.close(fd)
 print('Mac gate occupied; no check, compiler or publication started')
 raise SystemExit(75)
child=None
try:
 assert (info.st_dev,info.st_ino)==(gate.lstat().st_dev,gate.lstat().st_ino)
 def git(*args):return subprocess.check_output(['git',*args],text=True).strip()
 def stop(signum,frame):
  if child is not None and child.poll() is None:
   os.killpg(child.pid,signal.SIGTERM)
   try:child.wait(timeout=15)
   except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
  raise SystemExit(143)
 signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
 assert git('branch','--show-current')=='codex/gw120-schema59-integration'
 assert git('status','--porcelain')=='' and git('rev-parse','HEAD')==expected
 helper=base.parent/'audit58-resume.UeFZoBlF'/'git-network.py'
 with (run/'fetch.log').open('wb') as out:
  subprocess.run(['python3',str(helper),'fetch'],stdout=out,stderr=subprocess.STDOUT,check=True,timeout=110)
 assert git('rev-parse','origin/main')==expected_main, 'Main changed; inspect source delta and actual migration tail before proceeding'
 subprocess.run(['git','merge-base','--is-ancestor',expected_main,expected],check=True)
 oldfiles=git('ls-tree','-r','--name-only','origin/main','services/control/migrations/postgresql').splitlines()
 ups=[x for x in oldfiles if x.endswith('.up.sql')]
 assert len(ups)==58 and max(int(Path(x).name.split('_',1)[0]) for x in ups)==58
 for path in oldfiles:
  assert subprocess.check_output(['git','show','origin/main:'+path])==Path(path).read_bytes(), 'Earlier numbered migration changed'
 admission=json.loads((base/'publication'/'admission.json').read_text())
 assert admission['source_commit']==qualified and admission['root_gate_exit']==0 and admission['matrix_exit']==0
 for path,digest in admission['raw_evidence_sha256'].items():assert hashlib.sha256(Path(path).read_bytes()).hexdigest()==digest
 for suffix in ('up','down'):
  p=repo/'services/control/migrations/postgresql'/('000059_model_alias_description.'+suffix+'.sql')
  assert hashlib.sha256(p.read_bytes()).hexdigest()==admission[suffix+'_sha256']
 diff=git('diff','--name-only',qualified,'HEAD').splitlines()
 product=[p for p in diff if not(p.startswith('docs/') and p.endswith('.md'))]
 ignored=['services/control/internal/modules/productagent/http/dispatch_repository_integration_test.go','services/control/internal/modules/productagent/http/guarded_dispatch_repository_integration_test.go']
 assert sorted(product)==ignored, 'Changed build inputs require renewed affected checks'
 env=os.environ.copy()
 env.update({'GOTOOLCHAIN':'local','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'2','GOFLAGS':'-p=1','CARGO_NET_OFFLINE':'true'})
 env['PATH']=str(base/'offline-tools')+':'+env['PATH']
 env['PYTHON']=subprocess.check_output(['uv','python','find','3.12'],text=True).strip()
 for target in ('darwin','linux'):
  selected=env.copy();selected.update({'GOOS':target,'GOARCH':'arm64' if target=='darwin' else 'amd64','CGO_ENABLED':'0'})
  raw=subprocess.check_output(['go','-C','services/control','list','-json','./internal/modules/productagent/http'],env=selected)
  (run/('go-list-'+target+'.json')).write_bytes(raw)
  package=json.loads(raw)
  for path in ignored:
   name=Path(path).name
   assert name in package.get('IgnoredGoFiles',[]) and all(name not in package.get(k,[]) for k in ('GoFiles','TestGoFiles','XTestGoFiles'))
 proof={'head':expected,'origin_main':expected_main,'qualified_source':qualified,'changed_paths':diff,'default_product_inputs_unchanged':True,'root_check_build_test':'reused unchanged qualified default build inputs','new_check':'pending','publish_requested':publish}
 (run/'source-equivalence.json').write_text(json.dumps(proof,indent=2)+'\n')
 print('GW-120 candidate '+expected+'; final gate '+str(run),flush=True)
 with (run/'check.log').open('wb') as out:
  child=subprocess.Popen(['caffeinate','-i','make','check'],env=env,stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
  code=child.wait(timeout=900)
 (run/'check-exit').write_text(str(code)+'\n')
 if code:raise SystemExit(code)
 assert git('status','--porcelain')=='' and git('rev-parse','HEAD')==expected
 assert git('rev-parse','origin/main')==expected_main
 proof['new_check']='passed'
 (run/'source-equivalence.json').write_text(json.dumps(proof,indent=2)+'\n')
 if publish:
  with (run/'push.log').open('wb') as out:subprocess.run(['python3',str(helper),'push'],stdout=out,stderr=subprocess.STDOUT,check=True,timeout=110)
  with (run/'confirm.log').open('wb') as out:subprocess.run(['python3',str(helper),'fetch'],stdout=out,stderr=subprocess.STDOUT,check=True,timeout=110)
  remote=git('rev-parse','origin/main')
  subprocess.run(['git','merge-base','--is-ancestor',expected,remote],check=True)
  (run/'published').write_text(expected+'\n'+remote+'\n')
  print('Published by fast-forward '+expected+'; confirmed remote '+remote,flush=True)
 else:print('Final source and make check admission passed; main unchanged',flush=True)
finally:
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=15)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 fcntl.flock(fd,fcntl.LOCK_UN);os.close(fd)
