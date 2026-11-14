from pathlib import Path
import hashlib,json,subprocess,sys,tarfile
root=Path(sys.argv[1]);checkout=Path(sys.argv[2]);client=root/'installed-a4f'
assert not (root/'input-manifest.json').exists(), 'Delivered inputs are immutable; create a new package instead'
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=checkout,text=True).strip()
info=json.loads((root/'preparation.json').read_text());source=info['source']
assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
paths=['web','api/openapi','pkg/capabilities/declaration.json','tests/fixtures','tools/codegen/web-api.mjs','tools/codegen/web-help.mjs','Makefile']
assert not git('diff','--name-only',info['originalQualifiedSource'],source,'--',*paths)
assert not git('diff','--name-only',source,info['preparedSource'],'--',*paths)
original=json.loads((root/'original-qualification.json').read_text())
assert original['source']==info['originalQualifiedSource']
assert original['stats']['console']['expected']==1134 and original['stats']['console']['skipped']==28
assert original['stats']['portal']['expected']==410 and original['stats']['portal']['skipped']==10
for app in ['console','portal']:
 report=json.loads((root/('original-'+app+'-report.json')).read_text())
 assert report['stats']==original['stats'][app]
 assert report['stats']['unexpected']==0 and report['stats']['flaky']==0
assert (client/'binding.json').exists()
assert json.loads((client/'binding.json').read_text())['source']==source
assert '18 input guard selftests passed' in (client/'input-selftest.log').read_text()
assert '4 output preservation selftests passed' in (client/'output-selftest.log').read_text()
for name in ['discovery.log','help-discovery.log','scope-discovery.log']:
 assert 'Total: 1 test' in (client/name).read_text(),name
p=client/'README.md';p.write_text(p.read_text().replace('At candidate a4f1120d (only after the new selftest logs pass):','At candidate a4f1120d:'))
files={}
for app in ['console','portal']:
 dist=checkout/'web/apps'/app/'dist'
 for p in sorted(dist.rglob('*')):
  if p.is_file():
   assert not p.is_symlink(),p
   files[str(p.relative_to(checkout))]=sha(p)
assert files and any('/console/' in p for p in files) and any('/portal/' in p for p in files)
old_console=Path('/Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy/old-console-dist-manifest.json')
baseline=json.loads(old_console.read_text())
current={p.removeprefix('web/apps/console/dist/'):digest for p,digest in files.items() if p.startswith('web/apps/console/dist/')}
assert current==baseline,'Rebuilt Console assets differ from qualified c864 bytes; inspect before packaging'
portal_baseline=json.loads((root/'retained-portal-manifest.json').read_text())
current_portal={p.removeprefix('web/apps/portal/dist/'):digest for p,digest in files.items() if p.startswith('web/apps/portal/dist/')}
assert current_portal==portal_baseline,'Rebuilt Portal assets differ from retained c864 bytes; inspect before packaging'
asset=root/'qualified-a4f-web-build.tar.gz'
with tarfile.open(asset,'w:gz') as out:
 for name in files:out.add(checkout/name,arcname=name,recursive=False)
oldroot=Path('/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113')
old_hashes={'qualified-be62-web-build.tar.gz':'a4bd543164852f5c381227da5bdb2af4d40777079d080856b087c743e0a44434','installed-entry-be62-inputs.tar.gz':'c2cd577bb55a89ae57f4c542c26f8ffc187b0948f70406ac661ba3a7b0747ba4','installed-entry.md':'082c76f583fc3d58d2cd4e0f58d6c5a798ff528deb8df930891169723676dd48','installed-entry-manifest.json':'652456d2046922817e40bb58e7a367fefac22c222a230e58732630117ef6dbef'}
for name,digest in old_hashes.items():assert sha(oldroot/name)==digest,name
client_files={p.name:sha(p) for p in sorted(client.iterdir()) if p.is_file() and not p.is_symlink()}
assert not any('storage' in name.lower() or 'cookie' in name.lower() for name in client_files)
archive=root/'installed-entry-a4f-inputs.tar.gz'
with tarfile.open(archive,'w:gz') as out:
 for name in client_files:out.add(client/name,arcname='installed-entry/'+name,recursive=False)
manifest={**info,'state':'Fresh immutable a4f assets and rebound code/selftests only; preparation is not frozen or installed','qualifiedFullGate':{'source':original['source'],'run':original['run'],'platform':'Mac arm64','stats':original['stats'],'reports':{app:sha(root/('original-'+app+'-report.json')) for app in ['console','portal']},'originalAuthorSeries':(root/'original-author-series').read_text().splitlines(),'originalAuthorPatchSha256':sha(root/'original-author-series.patch')},'assetArchive':{'path':str(asset),'sha256':sha(asset),'fileCount':len(files),'bytes':asset.stat().st_size},'assets':files,'clientArchive':{'path':str(archive),'sha256':sha(archive),'bytes':archive.stat().st_size},'clientFiles':client_files,'newSelftests':{'inputGuards':18,'outputPreservation':4,'typescript':'passed','discovered':{'gw120':1,'help':1,'scope':1},'browserStarted':False,'serverUsed':False,'realCredentialsUsed':False},'expectedRuntimeSchema':60,'expectedStableTables':120,'runtimeImage':None,'installedOrigins':None,'runtimeObjects':None,'credentialReferences':None,'installedBrowserRun':False,'independentSQLRun':False,'be62FilesUnchanged':old_hashes,'requiredOrder':['GW120','Admin Help','Portal Help','Scope','independent SQL','CLI','original OBS7']}
(root/'input-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
entry=f'''# a4f Web and installed-client preparation\n\nSource: `{source}`. DEL preparation: `{info['preparedSource']}` (not frozen, built or installed by this work). Web tree `{info['webTree']}`; OpenAPI tree `{info['openapiTree']}`.\n\n## Original qualification\n\nWeb/OpenAPI, generators, fixtures, capability declaration and Makefile inputs are byte-identical to original full-gate source `{original['source']}` and published `c864fbb10294fa36bdbf4256fe1601f8358ad1f8`. Original-author commits/patch and raw1134 Console/410 Portal reports are retained alongside this package;38 existing installation-dependent cases skipped, with no unexpected/flaky/retried browser case. This does not apply the newer audit-retry development gate to this older tree.\n\nBoth app builds were recreated offline from the clean a4f worktree with its own workspace dependency links. Every Console byte matches the archived c864 build; every Portal byte matches the retained c864 build. `{asset.name}` contains {len(files)} production files and retains the original installer layout. Its SHA-256 is `{sha(asset)}`. The input manifest records every asset hash.\n\n`{archive.name}` contains only the newly rebound client code/configuration and new selftest/discovery logs. Its SHA-256 is `{sha(archive)}`. No old session, cookie, storage state, credential file or prior run output was copied. New checks passed TypeScript no-emit,18 input guards,4 output-preservation guards and one GW120/Help/Scope discovery each. No browser, server or real credential was used by those checks.\n\n## Before an installed run\n\nThe a4f and current28f preparation browser inputs also match exactly. After DEL chooses the actual full frozen source, rebind a COPY of this client to that clean worktree and rerun checks under the permanent Mac lock. Supply the new immutable image digest, live schema60/stable tables120 proof, exact ready JSON/hash, distinct verified HTTPS origins, owned alias/deployment/workspace/foreign-canary IDs, dedicated Admin/member credential-file references, TLS pins/host rules and protected Scope fixture. The template remains intentionally invalid until those inputs exist. No runtime, URL, object or credential is invented here.\n\nRun `python3 run.py gw120`, then `help-admin`, `help-portal`, then `scope` through the shared Mac wrapper, as described in installed-entry/README.md. Each invocation creates fresh0700 evidence; read its screenshots. Scope uses the bound product test and gives that run's scope-proof.json to DEL's same existing native entry:\n\n`/bundle/scope.test -test.run=^TestTemporaryInstalledScopeProof$ -test.v -test.timeout=60s`\n\nThrough DEL's exact fixture execution prefix it independently checks the existing foreign canary, zero foreign membership and no success/accepted audit for the two UI refusal IDs. GW separately compares same-source description intent/revision/API/permission/DB state. Keep the order GW120 -> Help -> Scope -> independent SQL -> CLI -> original OBS7. Close own browser contexts, clients and polling before the quiet boundary. Actual installed browser, SQL, CLI, OBS and new permission variants remain open.\n\nThe be62 two tar files, entry, manifest and frozen worktree remain unchanged. Do not use these a4f inputs as a new audit-retry asset build or silently advance them to another Web tree.\n'''
(root/'entry.md').write_text(entry)
(root/'entry-digests.json').write_text(json.dumps({'inputManifest':sha(root/'input-manifest.json'),'entry':sha(root/'entry.md'),'assetArchive':sha(asset),'clientArchive':sha(archive)},indent=2)+'\n')
print(json.dumps({'root':str(root),'assets':len(files),'assetSha256':sha(asset),'clientSha256':sha(archive)}))
