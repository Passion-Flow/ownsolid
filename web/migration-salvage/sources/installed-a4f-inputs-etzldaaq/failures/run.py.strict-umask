#!/usr/bin/env python3
"""Run only against a newly bound installation; preserve every invocation separately."""
from pathlib import Path
from datetime import datetime, timezone
import hashlib,json,os,stat,subprocess,sys,tempfile
ROOT=Path(__file__).resolve().parent

def fresh_output(parent):
    parent=Path(parent)
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    info=parent.lstat()
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ValueError('Run parent must be a private owned directory')
    return Path(tempfile.mkdtemp(prefix=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ-'),dir=parent))

if sys.argv[1:]==['--selftest']:
    with tempfile.TemporaryDirectory(prefix='ow-gwo-',dir='/private/tmp') as d:
        first=fresh_output(d);second=fresh_output(d)
        assert first != second and list(first.iterdir()) == [] and list(second.iterdir()) == []
        assert stat.S_IMODE(first.stat().st_mode)==0o700
        unsafe=Path(d)/'unsafe';unsafe.mkdir(mode=0o755)
        try:fresh_output(unsafe)
        except ValueError:pass
        else:raise AssertionError('Unsafe parent accepted')
        linked=Path(d)/'linked';linked.symlink_to(first,target_is_directory=True)
        try:fresh_output(linked)
        except ValueError:pass
        else:raise AssertionError('Symlink parent accepted')
    print('4 output preservation selftests passed; no browser or server was used')
    raise SystemExit(0)
step=sys.argv[1] if len(sys.argv)==2 else 'gw120'
if len(sys.argv)>2 or step not in ('gw120','help-admin','help-portal','scope') or not os.environ.get('OWNSOLID_GW120_FIXTURE_FILE'):
    raise SystemExit('Set the normalized fixture, pinned TLS and host rules; use run.py [gw120|help-admin|help-portal|scope]')
fixture_path=Path(os.environ['OWNSOLID_GW120_FIXTURE_FILE'])
fixture_info=fixture_path.lstat()
if not stat.S_ISREG(fixture_info.st_mode) or fixture_info.st_uid != os.getuid() or fixture_info.st_mode & 0o077:
    raise SystemExit('Fixture must be a regular owned private file')
fixture=json.loads(fixture_path.read_text())
os.umask(0o077)
output=fresh_output(ROOT/'runs')
env=os.environ.copy();env['OWNSOLID_GW120_OUTPUT_DIR']=str(output)
config='playwright.config.ts'
if step.startswith('help-'):
    audience=step.removeprefix('help-')
    actor=fixture['admin'] if audience=='admin' else fixture['member']
    env.update(OWNSOLID_LIVE_AUDIENCE=audience,OWNSOLID_LIVE_ORIGIN=fixture['adminOrigin'] if audience=='admin' else fixture['portalOrigin'],OWNSOLID_LIVE_EMAIL=actor['email'],OWNSOLID_LIVE_PASSWORD_FILE=actor['passwordFile'])
    config='help.config.ts'
elif step=='scope':
    original=Path(env.get('OWNSOLID_LIVE_PORTAL_SCOPE_FILE',''))
    info=original.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise SystemExit('Scope fixture must be a regular owned private file')
    copied=output/'scope-input.json';copied.write_bytes(original.read_bytes());copied.chmod(0o600)
    env.update(OWNSOLID_LIVE_ORIGIN=fixture['portalOrigin'],OWNSOLID_LIVE_PORTAL_SCOPE_FILE=str(copied))
    config='scope.config.ts'

binding=json.loads((ROOT/'binding.json').read_text())
manifest={'step':step,'binding':binding,'createdAt':datetime.now(timezone.utc).isoformat(),'clientFiles':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(ROOT.iterdir()) if p.is_file() and p.suffix in ('.ts','.py','.mjs')},'scope':'Installed browser flow only; live image/schema binding and independent SQL belong to DEL'}
with (output/'invocation.json').open('x') as f:json.dump(manifest,f,indent=2);f.write('\n')
print(str(output),flush=True)
result=subprocess.run(['node',str(ROOT/'node_modules/@playwright/test/cli.js'),'test','--config',str(ROOT/config)],cwd=ROOT,env=env)
(output/'exit-status').write_text(str(result.returncode)+'\n')
raise SystemExit(result.returncode)
