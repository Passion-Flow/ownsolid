from pathlib import Path
import hashlib,json,subprocess,sys,tarfile
root=Path(sys.argv[1]);checkout=Path(sys.argv[2]);client=root/'installed-aud108'
assert not (root/'input-manifest.json').exists(),'Delivered inputs are immutable; create a new package'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def git(*args):return subprocess.check_output(['git',*args],cwd=checkout,text=True).strip()
info=json.loads((root/'preparation.json').read_text());source=info['source']
assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
paths=['web','api','pkg/capabilities/declaration.json','tests/fixtures','tools/codegen/web-api.mjs','tools/codegen/web-help.mjs','Makefile']
assert not git('diff','--name-only',info['originalQualifiedSource'],source,'--',*paths)
original=json.loads((root/'original-qualification.json').read_text())
assert original['source']==info['originalQualifiedSource'] and original['newRecoveryCases']==41
assert original['stats']['console']['expected']==1191 and original['stats']['console']['skipped']==28
assert original['stats']['portal']['expected']==410 and original['stats']['portal']['skipped']==10
for app in ['console','portal']:
 report=json.loads((root/('original-'+app+'-report.json')).read_text())
 assert report['stats']==original['stats'][app] and not report['errors']
 assert report['stats']['unexpected']==0 and report['stats']['flaky']==0
assert json.loads((client/'binding.json').read_text())['source']==source
assert '18 input guard selftests passed' in (client/'input-selftest.log').read_text()
assert '4 output preservation selftests passed' in (client/'output-selftest.log').read_text()
for name in ['discovery.log','help-discovery.log','scope-discovery.log']:
 assert 'Total: 1 test' in (client/name).read_text(),name
files={}
for app in ['console','portal']:
 for p in sorted((checkout/'web/apps'/app/'dist').rglob('*')):
  if p.is_file():
   assert not p.is_symlink(),p
   files[str(p.relative_to(checkout))]=sha(p)
assert files==json.loads((root/'qualified-assets.json').read_text()),'Rebuilt assets differ from the fully qualified production bytes; inspect before packaging'
asset=root/'qualified-aud108-web-build.tar.gz'
assert asset.exists(), 'Qualified asset archive must already be preserved'
with tarfile.open(asset,'r:gz') as archived:
 assert {m.name:hashlib.sha256(archived.extractfile(m).read()).hexdigest() for m in archived.getmembers() if m.isfile()}==files
client_files={p.name:sha(p) for p in sorted(client.iterdir()) if p.is_file() and not p.is_symlink()}
assert len(client_files)==23,len(client_files)
assert not any('storage' in name.lower() or 'cookie' in name.lower() for name in client_files)
archive=root/'installed-entry-aud108-inputs.tar.gz'
with tarfile.open(archive,'w:gz') as out:
 for name in client_files:out.add(client/name,arcname='installed-entry/'+name,recursive=False)
prior=Path('/Users/passion./.local/state/ownsolid/web-scopes/installed-a4f-inputs-etzldaaq')
prior_files={'inputManifest':'input-manifest.json','entry':'entry.md','assetArchive':'qualified-a4f-web-build.tar.gz','clientArchive':'installed-entry-a4f-inputs.tar.gz'}
for key,name in prior_files.items():assert sha(prior/name)==info['priorA4fInputDigests'][key],name
oldroot=Path('/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113')
be62=json.loads((prior/'input-manifest.json').read_text())['be62FilesUnchanged']
for name,digest in be62.items():assert sha(oldroot/name)==digest,name
manifest={**info,'state':'Fresh source-bound assets and client selftests; DEL has not frozen or installed this source','qualifiedFullGate':{'source':original['source'],'run':original['run'],'platform':'Mac arm64','stats':original['stats'],'newAuditRecoveryCases':41,'screenshotsRead':original['imagesRead'],'reports':{app:sha(root/('original-'+app+'-report.json')) for app in ['console','portal']},'originalAuthorSeries':(root/'original-author-series').read_text().splitlines(),'originalAuthorPatchSha256':sha(root/'original-author-series.patch')},'assetArchive':{'path':str(asset),'sha256':sha(asset),'fileCount':len(files),'bytes':asset.stat().st_size},'assets':files,'clientArchive':{'path':str(archive),'sha256':sha(archive),'bytes':archive.stat().st_size},'clientFiles':client_files,'newSelftests':{'inputGuards':18,'outputPreservation':4,'typescript':'passed','discovered':{'gw120':1,'help':1,'scope':1},'browserStarted':False,'serverUsed':False,'realCredentialsUsed':False},'runtimeImage':None,'installedOrigins':None,'runtimeObjects':None,'credentialReferences':None,'installedBrowserRun':False,'independentSQLRun':False,'be62FilesUnchanged':be62,'requiredOrder':['GW120','Admin Help','Portal Help','Scope','independent SQL','CLI','original OBS7'],'olderPreparationCompatibility':'The prior a4f/e5c and 5893 candidate preparations exclude this audit recovery Web tree; a new DEL preparation and freeze are required. Do not relabel any older package or installation.'}
(root/'input-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
entry=f'''# Explicit audit-read recovery installation inputs

Published source: `{source}`. Original full-gate source: `{original['source']}`.
Web tree: `{info['webTree']}`. OpenAPI tree: `{info['openapiTree']}`.

The original Mac arm64 gate passed make check, make build and make test:
1,191 Console and 410 Portal Chromium cases passed, with 28/10 existing
conditional skips and no unexpected/flaky/retried case. All 41 explicit audit
recovery cases passed. Eighteen screenshots were actually read. Original-author
series/patch, raw browser reports and qualification inputs accompany this package.
The exact published Web, API, fixtures, generators, capabilities and Makefile
inputs match that complete gate. These are scripted Web results; installed
commit-loss/Scope/GW120/SQL/CLI/OBS acceptance remains separate and open.

Both production builds were recreated offline from the clean detached worktree,
using its own workspace dependency links. All {len(files)} asset hashes match
the preserved archive of qualified production bytes; that archive was kept unchanged. `{asset.name}` SHA-256: `{sha(asset)}`.
The input manifest lists every regular asset and its hash.

`{archive.name}` SHA-256: `{sha(archive)}`. Its 23 files contain only code,
new source bindings/configuration and newly run selftest/discovery logs. No prior
browser run, session, cookie, storage state or credential was copied. New no-emit
TypeScript,18 input guards,4 output-preservation guards and GW120/Help/Scope
discovery each passed. Discovery started no browser and used no server or real
credential. The client template intentionally remains invalid for a live run.

The prior a4f/e5c package and DEL5893 candidate exclude this Web tree. DEL must
prepare and freeze an updated full source before building a new immutable image.
This package is not a freeze or installation GO. No old image, session or ready
record may be relabelled. The a4f and be62 delivered artifacts remain unchanged.

Before an installed run, rebind a COPY to DEL's actual new clean frozen full SHA
and rerun checks under the permanent Mac lock. Supply the new immutable image,
live schema60/stable tables120 proof, ready JSON plus exact byte hash, distinct
verified HTTPS Admin/Portal origins, owned alias/deployment/workspace/foreign
canary IDs, dedicated Admin/member private password JSON references, TLS pins,
host rules and protected Scope fixture. Schema/table values above are source
constants, not live SQL observations. No runtime input is invented here.

Run through the shared Mac wrapper in this order: GW120, Admin Help, Portal Help,
Scope, independent SQL, CLI, original OBS7. See installed-entry/README.md and
python3 run.py STEP. Each browser invocation creates fresh0700 evidence; read
all resulting screenshots. Scope's same-run scope-proof.json goes to DEL's
existing native entry through its exact fixture execution prefix:

`/bundle/scope.test -test.run=^TestTemporaryInstalledScopeProof$ -test.v -test.timeout=60s`

Independent SQL must retain the existing foreign canary, zero foreign membership
and no accepted/success audit for the two UI refusal UUIDs. GW independently
compares same-source description intent/revision/API/permission/DB state. Close
own browser contexts, tunnels and polling before OBS's quiet boundary. This
preparation has run no installed browser, SQL, CLI, OBS or GPU acceptance.
'''
(root/'entry.md').write_text(entry)
(root/'entry-digests.json').write_text(json.dumps({'inputManifest':sha(root/'input-manifest.json'),'entry':sha(root/'entry.md'),'assetArchive':sha(asset),'clientArchive':sha(archive)},indent=2)+'\n')
print(json.dumps({'root':str(root),'source':source,'assets':len(files),'assetSha256':sha(asset),'clientSha256':sha(archive)}))
