"""List or restore archived source into a new directory without overwriting files."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--archive',default='63a32096f9e714e72354504aef2f31fee9dbef6d')
parser.add_argument('--prefix',help='Exact original path prefix from the source manifest')
parser.add_argument('--destination',type=Path,help='New, nonexistent directory')
args=parser.parse_args()
def git(*a):return subprocess.run(['git',*a],capture_output=True,check=True).stdout
paths=git('ls-tree','-r','--name-only',args.archive,'source-manifests').decode().splitlines()
records=[]
for path in paths:
    records.extend(json.loads(git('show',args.archive+':'+path)))
if not args.prefix:
    roots={p['path'].split('/source/')[0]+'/source/' if '/source/' in p['path'] else str(Path(p['path']).parent)+'/' for p in records}
    print('\n'.join(sorted(roots)))
    raise SystemExit(0)
selected=[r for r in records if r['path'].startswith(args.prefix)]
if not selected:raise SystemExit('No source paths match the prefix')
if args.destination is None:
    print(json.dumps({'matched_files':len(selected),'first_paths':[r['path'] for r in selected[:10]]},indent=2))
    raise SystemExit(0)
destination=args.destination.resolve()
if destination.exists():raise SystemExit('Destination already exists; choose a new directory')
relative=[Path(r['path'][len(args.prefix):]) for r in selected]
if any(p.is_absolute() or '..' in p.parts or not p.parts for p in relative):raise SystemExit('Unsafe relative source path')
if len(set(relative))!=len(relative):raise SystemExit('Duplicate relative source paths')
destination.mkdir(parents=True)
for row,path in zip(selected,relative):
    raw=git('show',args.archive+':source-blobs/'+row['blob'])
    if len(raw)!=row['bytes'] or hashlib.sha256(raw).hexdigest()!=row['sha256']:raise SystemExit('Source integrity verification failed')
    target=destination/path
    target.parent.mkdir(parents=True,exist_ok=True)
    with target.open('xb') as stream:stream.write(raw)
    target.chmod(0o755 if row['mode']=='100755' else 0o644)
print(json.dumps({'restored_files':len(selected),'destination':str(destination)}))
