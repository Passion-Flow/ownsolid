import ast,hashlib,json,os,pathlib,stat,subprocess,sys,tarfile,time
os.umask(0o077)
r=pathlib.Path('/root/lane-rel-tools');w=pathlib.Path('/root/lane-rel-identity-audit-contract')
d=r/'joint-api-review/delivery-2e64ab4c-c5e4f1cb'
source='2e64ab4c6c16bd84ae5c101b9de5444550760ff2';tree='3bdf79d7d76f096c989884190c32e4c08de20356';old_source='9be01cbbf0144a2f2537667834539de31e40f58f';main='bf448bcd070285e04ffec2474b3f32dcea998989'
entry_sha='f08c440fa6a53054ef08da78b7ca2e3a13fb5a5409edbc7d3e2f62348790fe9a';manifest_sha='60db5ee075c6b24e06d16a0453e851d140ffa44b88b2207310f911901b73f47c';bundle_sha='c5e4f1cbb7812b841de739c1524e6f3700d1e3aadfd5ddc5a2852784cda74a52';packet_sha='52a842955243ecfcf7efdf2afc8af87bd042ad4d0cd24095616121a451f0b468'
old_entry_sha='1a5f519cbbc52dec3b7dd4c31b199486a14674e7310f92678765a8e9bd04a15c'
frozen={'identity-audit-contract/joint-full-gate-manifest.json':'9a5e296cd3612ae6f9bdd36294148a83dfc4110e4319e837552f76975d096843','api-compat/joint-native-gate-9be01cbb-proof.json':'ea63d0f4d26a25744f322da5402381397c7fd14768c7afb8ab9b4c8509aaa5fe','api-compat/joint-native-gate-9be01cbb.log':'fbcc353ee78c27155e318b3c71fd4ec57fea98bf551080391dd094736c1044c3','identity-audit-contract/go-native-linux-proof.json':'cd648aa45ce2ab13f08838b9254a18b2151ca93c56f0201a3f1f9ca1d5e13cd1'}
started=time.monotonic();state={'purpose':'Exact source/artifact staging only; no runtime or test qualification','delivery_id':d.name,'source':source,'tree':tree,'main_base':main,'phase':'start','success':False,'entry_executed':False,'four_targets_started':False,'locks_opened_or_acquired':False,'new_go_node_pg_or_browser_run':False,'queue_created':False,'capacity_measured':False,'execution_authorized':False};errors=[]
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def write_new(path,value):
 with path.open('x') as out:json.dump(value,out,indent=2);out.write('\n')
def phase(name):
 state['phase']=name
 if d.is_dir():
  temp=d/'current-staging-state.new';temp.write_text(json.dumps(state,indent=2)+'\n');os.replace(temp,d/'current-staging-state.json')
 print('STAGE_PHASE',name,flush=True)
def git(*args,repo=w,seconds=15,allowed=(0,),input_bytes=None):
 remaining=220-(time.monotonic()-started)
 if remaining<5:raise TimeoutError('Staging metadata deadline exhausted')
 limit=min(seconds,remaining-4)
 result=subprocess.run(['timeout','--signal=TERM','--kill-after=3s',str(round(limit,3))+'s','git','-c','core.commitGraph=false','-c','pack.threads=1',*args],cwd=repo,stdin=subprocess.DEVNULL if input_bytes is None else None,input=input_bytes,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=limit+4)
 if result.returncode not in allowed:
  errors.append({'phase':state['phase'],'exit':result.returncode,'stderr_sha256':hashlib.sha256(result.stderr).hexdigest()})
  if d.is_dir():
   with (d/('git-diagnostic-'+str(len(errors))+'.log')).open('xb') as out:out.write(result.stderr)
  raise RuntimeError('Git metadata verification failed')
 return result.stdout
try:
 phase('connected-preflight')
 assert git('rev-parse','HEAD').decode().strip()==old_source,'Unexpected initial source'
 assert git('status','--porcelain')==b'','Initial source must be clean'
 canonical=r/'api-compat/full_native_gate.py'
 assert canonical.is_file() and not canonical.is_symlink() and sha(canonical)==old_entry_sha,'Executed entry changed'
 for name,wanted in frozen.items():assert sha(r/name)==wanted,'Frozen evidence changed'
 assert not os.path.lexists(d),'Delivery already has state; reconcile it read-only'
 d.parent.mkdir(parents=True,exist_ok=True);assert not d.parent.is_symlink();d.mkdir(mode=0o700)
 phase('receiving-unique-packet')
 packet=d/'delivery.tar.gz.partial';digest=hashlib.sha256();size=0
 with packet.open('xb') as out:
  while True:
   chunk=sys.stdin.buffer.read(65536)
   if not chunk:break
   size+=len(chunk);assert size<=242857,'Unexpected packet size';digest.update(chunk);out.write(chunk)
 assert size==242857 and digest.hexdigest()==packet_sha,'Packet transfer is incomplete or corrupt'
 os.replace(packet,d/'delivery.tar.gz');state['packet_sha256']=packet_sha
 write_new(d/'receipt-transfer.json',{'packet_sha256':packet_sha,'bytes':size,'entry_executed':False})
 phase('verifying-artifact-bytes')
 with tarfile.open(d/'delivery.tar.gz','r:gz') as archive:
  members=archive.getmembers();names=[item.name for item in members]
  assert len(names)==len(set(names)) and all(item.isfile() and pathlib.PurePosixPath(item.name).name==item.name for item in members),'Unexpected archive member'
  index_bytes=archive.extractfile('delivery-files-sha256.json').read();index=json.loads(index_bytes)
  assert set(names)==set(index)|{'delivery-files-sha256.json'},'Archive index does not match'
  for member in members:
   content=archive.extractfile(member).read()
   if member.name!='delivery-files-sha256.json':assert len(content)==index[member.name]['bytes'] and hashlib.sha256(content).hexdigest()==index[member.name]['sha256'],'Artifact SHA mismatch'
   with (d/member.name).open('xb') as out:out.write(content)
 assert sha(d/'joint-source-2e64ab4c.bundle')==bundle_sha and sha(d/'full_native_gate.prepared.py')==entry_sha and sha(d/'joint-full-gate-2e64ab4c-manifest.json')==manifest_sha
 ast.parse((d/'full_native_gate.prepared.py').read_text())
 manifest=json.loads((d/'joint-full-gate-2e64ab4c-manifest.json').read_text());composition=json.loads((d/'composition-proof.json').read_text());projection=json.loads((d/'projection-input-sha256.json').read_text())
 assert manifest['source']==source and manifest['tree']==tree and not manifest['execution_authorized']
 assert manifest['targets']==['web-install','check','build','test'] and len(manifest['carriers'])==2
 assert len(composition['owned_paths'])==19 and len(projection)==79
 ref='refs/lane-rel/retained-joint-gate-9be01cbb'
 existing=git('rev-parse','--verify','--quiet',ref,allowed=(0,1)).decode().strip()
 if existing:assert existing==old_source
 else:git('update-ref',ref,old_source,'0'*40)
 archived=r/'api-compat/full_native_gate.executed-9be-1a5f519c.py'
 if archived.exists():assert sha(archived)==old_entry_sha
 else:
  with archived.open('xb') as out:out.write(canonical.read_bytes())
 assert sha(archived)==old_entry_sha
 state['retained_old_source_ref']=ref;state['retained_executed_entry']=str(archived)
 phase('independent-bare-import')
 bare=d/'independent-import.git';git('init','--bare',str(bare))
 git('fetch','--no-tags',str(w),old_source+':refs/heads/baseline',repo=bare,seconds=90)
 assert not (bare/'objects/info/alternates').exists(),'Independent import must not use alternates'
 git('bundle','verify',str(d/'joint-source-2e64ab4c.bundle'),repo=bare)
 git('fetch','--no-tags',str(d/'joint-source-2e64ab4c.bundle'),'HEAD:refs/heads/candidate',repo=bare)
 git('symbolic-ref','HEAD','refs/heads/candidate',repo=bare)
 assert git('rev-parse','HEAD',repo=bare).decode().strip()==source
 assert git('rev-parse','HEAD^{tree}',repo=bare).decode().strip()==tree
 web_bundle=r/'identity-audit-contract/web-owner-c7ba6271.bundle';assert sha(web_bundle)=='2ec837b665b15d914f42a705de4b799176ad5cd0e46a688dd196b851f8644749'
 heads=[line.split() for line in git('bundle','list-heads',str(web_bundle),repo=bare).decode().splitlines()]
 web_ref=next(name for oid,name in heads if oid=='c7ba62718fa4cded20914c179473cb2a02bf2ea9')
 git('fetch','--no-tags',str(web_bundle),web_ref+':refs/receipts/original-web-c7',repo=bare)
 git('fsck','--connectivity-only','--no-dangling',source,repo=bare,seconds=60)
 phase('verifying-dag-and-owned-paths')
 ancestors=set(git('rev-list',source,repo=bare).decode().splitlines())
 for oid in [old_source,main,'47fa03253b0d2655be26925a7e4492547c3e9a42','a84c4eb0e2653a8a0b326d1aca8913175cfb0536','20e30dac8196548561b6fccca2bed19dd33068d9']:assert oid in ancestors
 for oid in ['c7ba62718fa4cded20914c179473cb2a02bf2ea9','f1ccc065f76008076786d54e2603f6db25756318','6a2dd96fb392444de755db1cf5bfb771867064b0']:assert oid not in ancestors
 author=lambda oid:git('show','-s','--format=%an%x00%ae%x00%aI',oid,repo=bare)
 assert author('c7ba62718fa4cded20914c179473cb2a02bf2ea9')==author('a84c4eb0e2653a8a0b326d1aca8913175cfb0536')
 patch_ids=[]
 for oid in ['c7ba62718fa4cded20914c179473cb2a02bf2ea9','a84c4eb0e2653a8a0b326d1aca8913175cfb0536']:
  patch=git('show','--format=','--binary',oid,repo=bare);patch_ids.append(git('patch-id','--stable',repo=bare,input_bytes=patch).split()[0])
 assert patch_ids[0]==patch_ids[1]==b'6b5926cf6cab24baa02564c25a0a823a9784d017'
 changed=git('diff','--name-only',main,source,repo=bare).decode().splitlines();assert changed==composition['owned_paths']
 for name,wanted in {**composition['file_sha256'],**projection,**composition['protected_five_hashes_verified']}.items():assert hashlib.sha256(git('show',source+':'+name,repo=bare)).hexdigest()==wanted,'Source file fingerprint mismatch'
 for name in [n for n in changed if n.startswith('web/')]:assert git('show',source+':'+name,repo=bare)==git('show','c7ba62718fa4cded20914c179473cb2a02bf2ea9:'+name,repo=bare)
 status_diff=git('diff','--unified=0',main,source,'--','docs/product/status.md',repo=bare).decode()
 for line in status_diff.splitlines():
  if line.startswith(('+','-')) and not line.startswith(('+++','---')):assert line[1:].startswith(('| Audit-read commit outcome contract (DEV-101, AUD-108, open) |','| Offline public API compatibility review (DEV-204, open) |'))
 archive_path=r/'identity-audit-contract/identity-sdk-inputs.tar.gz';assert sha(archive_path)=='6ba53a09b2f1fc3794f4f723366b05629ad7cc54a4dbecd81144f52a33365aa2'
 with tarfile.open(archive_path) as archive:
  metadata=json.load(archive.extractfile('generation.json'));hashes=metadata['file_sha256'];assert len(hashes)==122
  for name,wanted in hashes.items():
   member='sdk/'+name if 'sdk/'+name in archive.getnames() else name
   assert hashlib.sha256(archive.extractfile(member).read()).hexdigest()==wanted
 qualification=json.loads((d/'pointer20-verification.json').read_text());audit=json.loads((d/'pointer20-430-audit.json').read_text())
 assert qualification['source']=='20e30dac8196548561b6fccca2bed19dd33068d9' and qualification['focused_tests']==31 and qualification['audit_exit']==1
 assert sha(d/'pointer20-regression-tests.log')==qualification['focused_test_log_sha256'] and sha(d/'pointer20-430-audit.json')==qualification['audit_raw_sha256']
 assert len(audit['operations'])==430 and not audit['findings'] and [f['code'] for f in audit['compatibility_findings']]==['PUBLIC_RESPONSE_MODELS_CHANGED']
 state.update(independent_bare_import=True,independent_bare_no_alternates=True,dag_verified=True,original_author_preserved=True,web_stable_patch_id=patch_ids[0].decode(),owned_paths_verified=19,projection_inputs_verified=79,sdk_archive_files_verified=122,protected_consumer_helpers_verified=5,retained_pointer_test_source=qualification['source'],retained_pointer_test_cases=31,retained_430_audit_exit=1)
 write_new(d/'receipt-import-and-dag.json',dict(state))
 phase('switching-owned-clean-worktree')
 assert git('rev-parse','HEAD').decode().strip()==old_source and git('status','--porcelain')==b''
 git('fetch','--no-tags',str(d/'joint-source-2e64ab4c.bundle'),'HEAD:refs/lane-rel/staged-joint-2e64ab4c')
 git('switch','--detach',source)
 assert git('rev-parse','HEAD').decode().strip()==source and git('rev-parse','HEAD^{tree}').decode().strip()==tree
 for name,wanted in {**composition['file_sha256'],**projection,**composition['protected_five_hashes_verified']}.items():assert sha(w/name)==wanted
 assert git('status','--porcelain')==b''
 state['worktree_staged']=True
 phase('installing-verified-entry-with-retained-original')
 assert not canonical.is_symlink() and sha(canonical)==old_entry_sha
 temporary=canonical.with_name('full_native_gate.stage-2e64ab4c-new.py')
 with temporary.open('xb') as out:out.write((d/'full_native_gate.prepared.py').read_bytes())
 temporary.chmod(stat.S_IMODE(canonical.stat().st_mode));os.replace(temporary,canonical)
 assert sha(canonical)==entry_sha and sha(archived)==old_entry_sha
 for name,wanted in frozen.items():assert sha(r/name)==wanted
 execution={'source':source,'tree':tree,'manifest':str(d/'joint-full-gate-2e64ab4c-manifest.json'),'manifest_sha256':manifest_sha,'entry':str(canonical),'entry_sha256':entry_sha,'source_staged':True,'source_clean':True,'entry_executed':False,'execution_authorized':False,'four_targets_started':False,'new_resource_admission_attempted':False,'capacity_profile_measured':False,'actual_result':None,'note':'This is current staging/execution state. Frozen preparation manifests and legacy identity aliases are not current execution receipts.'}
 write_new(d/'execution-state.json',execution)
 state.update(source_head_after=source,source_tree_after=tree,source_clean=True,canonical_entry_sha256=entry_sha,manifest_sha256=manifest_sha,bundle_sha256=bundle_sha,old_frozen_evidence_preserved=frozen,execution_state=str(d/'execution-state.json'),success=True)
 phase('complete')
except BaseException as error:
 state['error_phase']=state['phase'];state['phase']='failed';state['error_type']=type(error).__name__;state['failure']=str(error);state['success']=False
finally:
 state['elapsed_seconds']=round(time.monotonic()-started,3);state['git_failures']=errors
 if d.is_dir():
  final=d/'staging-result.json'
  if not final.exists():write_new(final,state)
  receipt_sha=sha(final)
 else:receipt_sha=None
 print('STAGE_RESULT_JSON '+json.dumps({'receipt':state,'receipt_sha256':receipt_sha}),flush=True)
sys.exit(0 if state['success'] else 1)
