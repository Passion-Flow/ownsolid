
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
from pathlib import Path
import json,shutil,subprocess,sys,tempfile
root=Path(sys.argv[1]);work=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
previous=Path((root/'publication-current-path').read_text().strip())
assert (previous/'stage').read_text().strip()=='complete' and (previous/'exit-status').read_text().strip()=='0'
def git(*a):return subprocess.check_output(['git',*a],cwd=work,text=True).strip()
def run(a,path):
 with path.open('w') as out:subprocess.run(a,cwd=work,stdout=out,stderr=subprocess.STDOUT,check=True)
assert not git('status','--porcelain')
old=git('rev-parse','HEAD');assert old==(previous/'rebased-source').read_text().strip()
pub=Path(tempfile.mkdtemp(prefix='publication-doc-sync-',dir=root))
run(['git','fetch','origin'],pub/'fetch.log')
run(['git','rebase','origin/main'],pub/'rebase.log')
source=git('rev-parse','HEAD');changed=git('diff','--name-only',old,source).splitlines()
assert changed==['docs/research/directory-login.md'],changed
(pub/'before').write_text(old+'\n');(pub/'base').write_text(git('rev-parse','origin/main')+'\n');(pub/'rebased-source').write_text(source+'\n')
(pub/'browser-input-diff').write_text('')
(pub/'runtime-input-reuse.json').write_text(json.dumps({'previousSource':old,'currentSource':source,'exactTreeDifference':changed,'qualifiedPreviousRun':str(previous),'previousCheckBuildTestExit':0,'fullBrowserSource':json.loads((root/'final-qualification.json').read_text())['source'],'disposition':'Only an independent research document changed. All runtime, configuration, dependency, build and test inputs are byte-identical. Reuse the already passed build/native/unit/browser results and run make check again for the new documentation.'},indent=2)+'\n')
(pub/'stage').write_text('check\n')
run(['make','check'],pub/'check.log')
assert not git('status','--porcelain')
(pub/'stage').write_text('complete\n');(pub/'exit-status').write_text('0\n')
(root/'publication-current-path').write_text(str(pub)+'\n')
(pub/'original-author-series').write_text(git('log','--reverse','--format=%H %an <%ae> %s','origin/main..HEAD')+'\n')
(pub/'original-author-series.patch').write_bytes(subprocess.check_output(['git','format-patch','--stdout','origin/main..HEAD'],cwd=work))
run(['git','push','origin','HEAD:main'],pub/'push.log')
(root/'published-source').write_text(source+'\n')
print(json.dumps({'publishedSource':source,'publication':str(pub),'runtimeInputs':'byte-identical','newCheck':'passed'}))
