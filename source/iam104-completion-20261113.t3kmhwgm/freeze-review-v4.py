import hashlib,io,json,os,pathlib,shutil,subprocess,tarfile
p=pathlib.Path(__file__).parent
repo=pathlib.Path((p/'repository-path').read_text().strip())
review=p/'review-source-v4';review.mkdir(mode=0o700)
paths=json.loads((p/'product-review-paths.json').read_text())
check=pathlib.Path((p/'latest-owner-check').read_text().strip())
checked=json.loads((check/'source-hashes.json').read_text())
assert all(hashlib.sha256((repo/x).read_bytes()).hexdigest()==d for x,d in checked.items())
index=p/'product-review-v4.index'
assert not index.exists()
env=os.environ|{'GIT_INDEX_FILE':str(index)}
subprocess.run(['git','read-tree','HEAD'],cwd=repo,env=env,check=True)
subprocess.run(['git','add','--',*paths],cwd=repo,env=env,check=True)
tree=subprocess.check_output(['git','write-tree'],cwd=repo,env=env,text=True).strip()
patch=p/'product-owner-v4.patch'
patch.write_bytes(subprocess.check_output(['git','diff','--cached','--binary','--full-index','--',*paths],cwd=repo,env=env))
for name in paths:
 f=review/'owner'/name;f.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(repo/name,f)
inputs=['saml_claim_probe_test.go','saml_claim_probe_repository_test.go','saml_stage_probe_test.go','saml_stage_probe_repository_test.go','saml_protocol_probe_test.go','saml_protocol_fixture_test.go','saml_identity_probe_test.go','saml_completion_probe_test.go','saml_completion_probe_repository_test.go','candidate.sql','candidate.down.sql','saml_completion_live_provider_test.go','qualify-live-completion.py','live-module-sum-admission.log','overlay.json','base-source.json','control.mod','control.sum','qualified-module-selection.json','delta-license-inventory.json','product-owner-paths.json','product-review-paths.json','repository-path','qualify.py','manual-qualify.py','check-current-owner.py','freeze-review-v4.py']
for name in inputs:shutil.copy2(p/name,review/name)
shutil.copy2(patch,review/patch.name)
for f in check.iterdir():
 if f.is_file():
  target=review/'no-database-check'/f.name;target.parent.mkdir(exist_ok=True);shutil.copy2(f,target)
cache=pathlib.Path.home()/'go/pkg/mod'
for item in json.loads((p/'delta-license-inventory.json').read_text()):
 folder=item['path']+'@'+item['version']
 for entry in item['files']:
  f=cache/folder/entry['path'];assert hashlib.sha256(f.read_bytes()).hexdigest()==entry['sha256']
  out=review/'delta-licenses'/folder/entry['path'];out.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(f,out)
base=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
summary={'base':base,'review_tree':tree,'owner_paths':paths,'owner_committed':False,'formal_schema_changed':False,'routes_installed':False,'database_cases_executed':0,'database_qualification_passed':False,'combined_roots_authored_not_run':34,'new_completion_roots_authored_not_run':11,'real_provider_domain_roots_not_run':1,'real_provider_readiness_roots_not_run':1,'unit_roots_passed':5,'unit_leaves_passed':13,'integration_compile_only':True,'no_database_check':str(check),'patch_sha256':hashlib.sha256(patch.read_bytes()).hexdigest()}
(review/'review-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
hashes={str(f.relative_to(review)):hashlib.sha256(f.read_bytes()).hexdigest() for f in sorted(review.rglob('*')) if f.is_file()}
manifest=p/'review-source-v4-hashes.json';manifest.write_text(json.dumps(hashes,indent=2)+'\n')
archive=p/'review-source-v4.tar'
with tarfile.open(archive,'w') as tar:
 for name in sorted(hashes):
  raw=(review/name).read_bytes();item=tarfile.TarInfo('review-source-v4/'+name);item.size=len(raw);item.mode=0o600;item.mtime=0;item.uid=item.gid=0;tar.addfile(item,io.BytesIO(raw))
summary.update({'manifest_sha256':hashlib.sha256(manifest.read_bytes()).hexdigest(),'bundle_sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'bundle_bytes':archive.stat().st_size,'file_count':len(hashes),'bundle':str(archive)})
(p/'review-v4-summary.json').write_text(json.dumps(summary,indent=2)+'\n')
print(json.dumps({k:v for k,v in summary.items() if k not in ('owner_paths',)},indent=2))
