from pathlib import Path
import json,os,subprocess,sys
root=Path(sys.argv[1]);pub=Path(sys.argv[2]);work=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
def git(*a):return subprocess.check_output(['git',*a],cwd=work,text=True).strip()
source=(pub/'rebased-source').read_text().strip()
assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
assert git('diff','--name-only',(pub/'before').read_text().strip(),source).splitlines()==['docs/research/directory-login.md']
failed=pub/'check.log.initial-system-python39'
assert not failed.exists();(pub/'check.log').rename(failed)
(pub/'initial-check-disposition.json').write_text(json.dumps({'exit':2,'cause':'The standalone resume script omitted the documented PYTHON override and used system Python3.9, which cannot parse the existing match statement. This is a runner environment failure; no product code changed.','correction':'Use uv-managed Python3.12 and the existing offline build environment.','source':source},indent=2)+'\n')
python=subprocess.check_output(['uv','python','find','3.12'],text=True).strip()
env={**os.environ,'PYTHON':python,'PYTHONDONTWRITEBYTECODE':'1','GOPROXY':'off','GOSUMDB':'off','GOMAXPROCS':'3','GOFLAGS':'-p=1','CARGO_NET_OFFLINE':'true','CARGO_BUILD_JOBS':'2','RUST_TEST_THREADS':'2','VITEST_MAX_WORKERS':'2','npm_config_offline':'true'}
with (pub/'check.log').open('w') as out:subprocess.run(['make','check'],cwd=work,env=env,stdout=out,stderr=subprocess.STDOUT,check=True)
assert not git('status','--porcelain')
(pub/'stage').write_text('complete\n');(pub/'exit-status').write_text('0\n')
(root/'publication-current-path').write_text(str(pub)+'\n')
(pub/'original-author-series').write_text(git('log','--reverse','--format=%H %an <%ae> %s','origin/main..HEAD')+'\n')
(pub/'original-author-series.patch').write_bytes(subprocess.check_output(['git','format-patch','--stdout','origin/main..HEAD'],cwd=work))
with (pub/'push.log').open('w') as out:subprocess.run(['git','push','origin','HEAD:main'],cwd=work,stdout=out,stderr=subprocess.STDOUT,check=True)
(root/'published-source').write_text(source+'\n')
print(json.dumps({'publishedSource':source,'publication':str(pub),'runtimeInputs':'byte-identical','newCheck':'passed with documented Python3.12'}))
