from pathlib import Path
import hashlib,json,subprocess,tarfile,os
repo=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
root=Path(__file__).resolve().parent
sha='be62f58e846dcc654dafee65cba6cc7480890570'
frozen=repo.parent/'lane-web-acceptance-be62f58e'
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()==sha
assert not subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True).strip()
assert not subprocess.check_output(['git','diff','60b85e03',sha,'--','web','api/openapi'],cwd=repo)
if not frozen.exists():subprocess.run(['git','worktree','add','--detach',str(frozen),sha],cwd=repo,check=True)
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=frozen,text=True).strip()==sha
assert not subprocess.check_output(['git','status','--porcelain'],cwd=frozen,text=True).strip()
files=[p for app in ('console','portal') for p in sorted((repo/'web/apps'/app/'dist').rglob('*')) if p.is_file()]
assert files
manifest={'source':sha,'qualifiedFullGateSource':'60b85e037aa24fc0d30ae0195e6dffe8f59a6946','webAndOpenapiByteIdentical':True,'frozenWorktree':str(frozen),'buildOrigin':'Preserved existing complete-gate production assets; not rebuilt for later development','files':{str(p.relative_to(repo)):hashlib.sha256(p.read_bytes()).hexdigest() for p in files}}
archive=root/'qualified-be62-web-build.tar.gz'
assert not archive.exists()
with tarfile.open(archive,'w:gz') as tar:
 for p in files:tar.add(p,arcname=str(p.relative_to(repo)),recursive=False)
manifest['archiveSha256']=hashlib.sha256(archive.read_bytes()).hexdigest()
(root/'qualified-be62-build-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(json.dumps({'frozen':str(frozen),'files':len(files),'archive':str(archive),'sha256':manifest['archiveSha256']},indent=2))
