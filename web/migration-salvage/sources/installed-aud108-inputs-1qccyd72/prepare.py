from pathlib import Path
import hashlib,json,re,shutil,subprocess,sys
root=Path(sys.argv[1]);evidence=Path('/Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy')
work=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
old=Path('/Users/passion./.local/state/ownsolid/web-scopes/installed-a4f-inputs-etzldaaq')
assert not (root/'preparation.json').exists()
source=(evidence/'published-source').read_text().strip()
def git(*args):return subprocess.check_output(['git',*args],cwd=work,text=True).strip()
assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
qualified=json.loads((evidence/'final-qualification.json').read_text())
paths=['web','api','pkg/capabilities/declaration.json','tests/fixtures','tools/codegen/web-api.mjs','tools/codegen/web-help.mjs','Makefile']
assert not git('diff','--name-only',qualified['source'],source,'--',*paths)
pub=Path((evidence/'publication-current-path').read_text().strip())
assert (pub/'stage').read_text().strip()=='complete' and (pub/'exit-status').read_text().strip()=='0'
assert (pub/'rebased-source').read_text().strip()==source
checkout=work.parent/('lane-web-acceptance-'+source[:8])
assert not checkout.exists(),checkout
subprocess.run(['git','worktree','add','--detach',str(checkout),source],cwd=work,check=True)
client=root/'installed-aud108';client.mkdir(mode=0o700)
names=['README.md','bind.py','checks.sh','description.spec.ts','fixture.template.json','fixture.ts','input-selftest.mjs','package.json','parameters.spec.ts','run.py']
for name in names:shutil.copy2(old/'installed-a4f'/name,client/name)
p=client/'README.md';p.write_text(p.read_text().replace('a4f1120d',source[:8]))
shutil.copy2(evidence/'final-qualification.json',root/'original-qualification.json')
for app in ['console','portal']:
 shutil.copy2(Path(qualified['run'])/(app+'-report.json'),root/('original-'+app+'-report.json'))
for name in ['original-author-series','original-author-series.patch']:
 shutil.copy2(pub/name,root/name)
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assets={str(p.relative_to(work)):sha(p) for app in ['console','portal'] for p in sorted((work/'web/apps'/app/'dist').rglob('*')) if p.is_file()}
assert assets
assert json.loads((root/'qualified-assets.json').read_text())==assets
schema=(checkout/'services/control/internal/platform/database/database.go').read_text()
version=int(re.search(r'const SchemaVersion = (\d+)',schema).group(1));tables=int(re.search(r'const TableCount = (\d+)',schema).group(1))
assert (version,tables)==(60,120),(version,tables)
old_digests=json.loads((old/'entry-digests.json').read_text())
old_files={'inputManifest':'input-manifest.json','entry':'entry.md','assetArchive':'qualified-a4f-web-build.tar.gz','clientArchive':'installed-entry-a4f-inputs.tar.gz'}
for key,name in old_files.items():assert sha(old/name)==old_digests[key],name
info={'source':source,'worktree':str(checkout),'originalQualifiedSource':qualified['source'],'publishedSource':source,'webTree':git('rev-parse',source+':web'),'openapiTree':git('rev-parse',source+':api/openapi'),'browserInputDiff':[],'codeOnlyClientFiles':names,'expectedRuntimeSchema':version,'expectedStableTables':tables,'priorA4fInputDigests':old_digests,'priorPreparationWebTree':json.loads((old/'input-manifest.json').read_text())['webTree'],'DELFreeze':None,'publishedNativeQualification':qualified['publishedIntegration']}
assert info['webTree']!=info['priorPreparationWebTree']
(root/'preparation.json').write_text(json.dumps(info,indent=2)+'\n')
print(json.dumps({'root':str(root),'source':source,'checkout':str(checkout),'webTree':info['webTree']}))
