from pathlib import Path
import hashlib,json,subprocess,sys,tarfile
root=Path(sys.argv[1]);client=root/'installed-help';prep=json.loads((root/'client-preparation.json').read_text());info=json.loads((root/'preparation.json').read_text());focused=json.loads((root/'focused-qualification.json').read_text())
assert not (root/'input-manifest.json').exists(),'Delivered input packages are immutable'
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assert json.loads((client/'binding.json').read_text())['source']==prep['source']
assert '18 input guard selftests passed' in (client/'input-selftest.log').read_text()
assert '4 output preservation selftests passed' in (client/'output-selftest.log').read_text()
for name in ['discovery.log','help-discovery.log','scope-discovery.log']:assert 'Total: 1 test' in (client/name).read_text(),name
assert (client/'typecheck.log').read_text()==''
for audience in ['console','portal']:
 stats=focused['results'][audience]['stats'];assert stats['expected']==2 and stats['unexpected']==0 and stats['skipped']==0 and stats['flaky']==0
assert focused['results']['negativePortal']['stats']['unexpected']==2
p=client/'README.md';s=p.read_text().replace(' (pending new checks):',':');p.write_text(s)
files={p.name:sha(p) for p in sorted(client.iterdir()) if p.is_file() and not p.is_symlink()}
assert len(files)==23,len(files)
assert not any('storage' in name.lower() or 'cookie' in name.lower() or 'credential' in name.lower() for name in files)
archive=root/('installed-entry-help-'+prep['source'][:8]+'-inputs.tar.gz')
with tarfile.open(archive,'w:gz') as out:
 for name in files:out.add(client/name,arcname='installed-entry/'+name,recursive=False)
assert sha(Path(info['assetArchive']))==info['assetArchiveSha256']
old=Path('/Users/passion./.local/state/ownsolid/web-scopes/installed-a4f-inputs-etzldaaq')
old_names={'inputManifest':'input-manifest.json','entry':'entry.md','assetArchive':'qualified-a4f-web-build.tar.gz','clientArchive':'installed-entry-a4f-inputs.tar.gz'}
old_digests=json.loads((old/'entry-digests.json').read_text())
for key,name in old_names.items():assert sha(old/name)==old_digests[key]
be62=json.loads((old/'input-manifest.json').read_text())['be62FilesUnchanged'];prior=Path('/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113')
for name,digest in be62.items():assert sha(prior/name)==digest,name
full_path=Path('/Users/passion./.local/state/ownsolid/web-scopes/aud108-explicit-retry-s0ycu2hy/final-qualification.json');full=json.loads(full_path.read_text())
manifest={**prep,'state':'New client selftests passed; qualified byte-identical production assets reused; no new installed acceptance','focusedHelp':focused,'priorFullWebGate':{'source':full['source'],'run':full['run'],'stats':full['stats'],'qualificationFile':str(full_path),'qualificationSha256':sha(full_path),'scope':'Prior complete source gate, not a complete gate of the corrected shared helper'},'newCompleteGateRun':False,'assetArchive':{'path':info['assetArchive'],'sha256':info['assetArchiveSha256'],'fileCount':info['assetFiles'],'source':info['assetSource'],'runtimeInputDelta':[],'reviewedNonRuntimeDelta':info['testPath']},'assetFileHashes':json.loads((root/'asset-file-hashes.json').read_text()),'clientArchive':{'path':str(archive),'sha256':sha(archive),'fileCount':len(files)},'clientFiles':files,'newSelftests':{'typescript':'passed','inputGuards':18,'outputPreservation':4,'discovery':{'GW120':1,'Help':1,'Scope':1},'browserStarted':False,'serverUsed':False,'realCredentialUsed':False},'authorSeries':(root/'original-author-series').read_text().splitlines(),'authorPatchSha256':sha(root/'original-author-series.patch'),'oldA4fDigestsUnchanged':old_digests,'oldBe62DigestsUnchanged':be62,'runtimeImage':None,'runtimeOrigins':None,'runtimeObjects':None,'credentialReferences':None,'DELFreeze':False,'installedBrowserRun':False,'independentSQLRun':False,'requiredOrder':['GW120','Admin Help','Portal Help','Scope','independent SQL','CLI','original OBS7'],'remaining':['Complete accurate-source gate after the Help correction','Native backend composition','DEL new immutable installation and live schema/table proof','Actual new browser/SQL/CLI/OBS acceptance']}
(root/'input-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
entry=f'''# Source-bound Help correction and installation inputs

Published candidate: `{prep['source']}`. Source Web tree: `{prep['sourceWebTree']}`.
OpenAPI tree: `{prep['openapiTree']}`.

The focused Help source `{focused['source']}` passed two locales in each entrance,
with no skip, failure, flaky result or retry. Two Portal controls first established
the remembered query and its settled input, then failed the former bare-URL
assertion. These controls use the same product assets; they do not assert that an
unmodified old suite must always fail. Both earlier MODEL full-run failures remain
preserved. The corrected helper still needs a complete gate in its qualified window.

Production runtime, generated inputs, dependencies, configuration and Makefile are
byte-identical to source `{info['assetSource']}`. Only the reviewed shared test
helper differs. Reuse the immutable 1,230-file asset archive:
`{info['assetArchive']}`
SHA-256 `{info['assetArchiveSha256']}`.
The source Web tree above includes test files and therefore differs from the
production base tree `{info['webTree']}`. No runtime rebuild or older e5c/be62
relabel is claimed. The prior c70 full1191/410 gate is retained separately.

New client archive: `{archive.name}`.
SHA-256 `{sha(archive)}`. Its23 regular files contain only copied code/templates,
new bindings/configuration and new checks. TypeScript no-emit,18 input guards,
4 output-preservation guards and one GW120/Help/Scope discovery each passed on this
exact candidate. No old run, session, cookie, storage or credential was copied.
Discovery started no browser and used no real server or credential.

This is preparation, not a DEL freeze or installation GO. Before the new installed
run, rebind a COPY to DEL's actual clean frozen full SHA and rerun its checks.
Supply the immutable image, live schema60/stable tables120 proof, ready JSON and
byte hash, distinct verified HTTPS origins, owned aliases/deployments/workspaces,
foreign canary, private dedicated Admin/member password JSON references, TLS pins,
host rules and the protected Scope fixture. The schema/table numbers above are
source constants; no live SQL was observed. No unknown runtime input is invented.

Run the existing client in this order under its proper shared windows: GW120,
Admin Help, Portal Help, Scope, independent SQL, CLI, original OBS7. Scope's own
scope-proof.json must go to DEL's same native entry:
`/bundle/scope.test -test.run=^TestTemporaryInstalledScopeProof$ -test.v -test.timeout=60s`.
Retain the foreign canary, zero foreign membership and no accepted/success audit
for both UI refusal UUIDs. GW independently checks same-source API/permission/DB.
Close owned browser contexts/tunnels/polling before OBS's quiet interval.
Actual installed browser, SQL, CLI, OBS and GPU acceptance remain open.
'''
(root/'entry.md').write_text(entry)
(root/'entry-digests.json').write_text(json.dumps({'entry':sha(root/'entry.md'),'inputManifest':sha(root/'input-manifest.json'),'assetArchive':info['assetArchiveSha256'],'clientArchive':sha(archive)},indent=2)+'\n')
print(json.dumps({'source':prep['source'],'clientArchive':str(archive),'clientSha256':sha(archive),'reusedAssetSha256':info['assetArchiveSha256'],'installedRun':False}))
