
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import hashlib,json,os,re,shutil,subprocess,sys
root=Path(sys.argv[1]);source=sys.argv[2];info=json.loads((root/'preparation.json').read_text());work=Path(info['worktree'])
assert not (root/'client-preparation.json').exists()
def git(cwd,*args):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
assert git(work,'rev-parse','HEAD')==source and not git(work,'status','--porcelain')
paths=['web','api','pkg/capabilities/declaration.json','tests/fixtures','tools/codegen/web-api.mjs','tools/codegen/web-help.mjs','Makefile']
assert not git(work,'diff','--name-only',info['focusedSource'],source,'--',*paths)
assert git(work,'diff','--name-only',info['assetSource'],source,'--',*paths).splitlines()==[info['testPath']]
if source==info['focusedSource']:
 frozen=Path(info['frozenWorktree'])
else:
 frozen=work.parent/('lane-web-acceptance-help-'+source[:8]);assert not frozen.exists()
 subprocess.run(['git','worktree','add','--detach',str(frozen),source],cwd=work,check=True)
 with (root/'published-frozen-install.log').open('w') as out:subprocess.run(['pnpm','install','--offline','--frozen-lockfile','--ignore-scripts'],cwd=frozen/'web',stdout=out,stderr=subprocess.STDOUT,check=True,timeout=15)
assert git(frozen,'rev-parse','HEAD')==source and not git(frozen,'status','--porcelain')
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
assets=json.loads((root/'asset-file-hashes.json').read_text())
a=Path('/Users/passion./.local/state/ownsolid/web-scopes/installed-aud108-inputs-1qccyd72')
for name,digest in assets.items():
 p=frozen/name
 if not p.exists():
  original=a/'qualified-production-assets'/name;assert sha(original)==digest
  p.parent.mkdir(parents=True,exist_ok=True);shutil.copy2(original,p)
 assert p.is_file() and not p.is_symlink() and sha(p)==digest
schema=(frozen/'services/control/internal/platform/database/database.go').read_text()
version=int(re.search(r'const SchemaVersion = (\d+)',schema).group(1));tables=int(re.search(r'const TableCount = (\d+)',schema).group(1));assert (version,tables)==(60,120)
client=root/'installed-help';client.mkdir(mode=0o700)
old=Path('/Users/passion./.local/state/ownsolid/web-scopes/installed-a4f-inputs-etzldaaq')
old_manifest=json.loads((old/'input-manifest.json').read_text())
names=['README.md','bind.py','checks.sh','description.spec.ts','fixture.template.json','fixture.ts','input-selftest.mjs','package.json','parameters.spec.ts','run.py']
for name in names:
 original=old/'installed-a4f'/name;assert sha(original)==old_manifest['clientFiles'][name]
 shutil.copy2(original,client/name)
p=client/'README.md';s=p.read_text().replace('a4f1120d',source[:8]);s=s.replace('At candidate '+source[:8]+':','At candidate '+source[:8]+' (pending new checks):');p.write_text(s)
subprocess.run([sys.executable,str(client/'bind.py'),str(frozen),source],check=True)
assert not git(frozen,'status','--porcelain')
prep={'source':source,'worktree':str(frozen),'sourceWebTree':git(frozen,'rev-parse','HEAD:web'),'openapiTree':git(frozen,'rev-parse','HEAD:api/openapi'),'productRuntimeBaseSource':info['assetSource'],'productRuntimeBaseWebTree':info['webTree'],'reviewedTestOnlyDelta':info['testPath'],'testSha256':sha(frozen/info['testPath']),'codeOnlyClientFiles':names,'expectedSchemaFromSource':version,'expectedTablesFromSource':tables,'liveSQLObserved':False,'state':'New source binding only; selftests must run before packaging'}
assert prep['testSha256']==info['fixedTestSha256']
(root/'client-preparation.json').write_text(json.dumps(prep,indent=2)+'\n')
print(json.dumps(prep))
