from pathlib import Path
import hashlib,json,sys
root=Path('/Users/passion./.local/state/ownsolid/web-scopes/help-saved-query-wwhvfal7');run=Path(sys.argv[1]);work=Path.cwd()
actual={str(p.relative_to(work)):hashlib.sha256(p.read_bytes()).hexdigest() for app in ['console','portal'] for p in sorted((work/'web/apps'/app/'dist').rglob('*')) if p.is_file()}
(run/'rebuilt-assets.json').write_text(json.dumps(actual,indent=2)+'\n')
assert actual==json.loads((root/'asset-file-hashes.json').read_text()),'Rebuilt assets differ; preserve the qualified archive and inspect before claiming reuse'
print('Fresh complete build matches all1230 preserved production asset bytes.')
