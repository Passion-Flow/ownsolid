"""Run the exact publication candidate under the shared foreground Mac window."""
import datetime,hashlib,json,os,plistlib,re,signal,socket,stat,subprocess,sys,time
from pathlib import Path

os.umask(0o077)
root=Path(__file__).resolve().parent
work=root/'work'
source='b967d7c8824b0efc00f8e5df0c50fa28ab3d8b68'
python=Path('/Users/passion./.local/share/uv/python/cpython-3.12.14-macos-aarch64-none/bin/python3.12')
proof={'source':source,'host':'darwin/arm64','check':False,'build':False,'test':False,'started_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'installed_acceptance':False,'linux_whole_gate':False}
child=None
device=None
volume_uuid=None
mount=Path('/Volumes/ownsolid-rel-b967d7c8-tests')
started=time.monotonic()

def git(*args):return subprocess.check_output(['git','-C',str(work),*args],text=True).strip()
def write_proof():
    proof['elapsed_seconds']=round(time.monotonic()-started,3)
    (root/'full-gate-proof.json').write_text(json.dumps(proof,indent=2)+'\n')
def stop_child():
    global child
    if child is None:return
    try:os.killpg(child.pid,signal.SIGTERM)
    except ProcessLookupError:pass
    try:child.wait(timeout=5)
    except subprocess.TimeoutExpired:
        os.killpg(child.pid,signal.SIGKILL);child.wait(timeout=5)
    child=None

def run_make(target,env):
    global child
    remaining=1200-(time.monotonic()-started)
    if remaining<=0:raise TimeoutError('Full gate deadline expired')
    print('FULL_GATE_TARGET',target,flush=True)
    child=subprocess.Popen(['make',target,'PYTHON='+str(python)],cwd=work,env=env,stdin=subprocess.DEVNULL,start_new_session=True)
    try:code=child.wait(timeout=remaining)
    except BaseException:stop_child();raise
    child=None
    proof[target+'_exit']=code
    if code:raise RuntimeError(target+' exited '+str(code))
    proof[target]=True
    write_proof()

def interrupted(signum,frame):raise KeyboardInterrupt('Gate interrupted by signal '+str(signum))
signal.signal(signal.SIGTERM,interrupted)
signal.signal(signal.SIGINT,interrupted)
audit=work/'tools/audit-verify/audit-verify'
audit_preexisting=audit.exists()
try:
    assert git('rev-parse','HEAD')==source and not git('status','--porcelain')
    proof['tree']=git('rev-parse','HEAD^{tree}')
    proof['cli_tree']=git('rev-parse','HEAD:tools/cli')
    assert proof['cli_tree']=='652ebc07fd797be5e2c88988850690fa9a488186'
    disk=os.statvfs('/');assert disk.f_bavail*disk.f_frsize>=15*1024**3
    pressure=subprocess.check_output(['memory_pressure','-Q'],text=True)
    match=re.search(r'free percentage:\s*(\d+)%',pressure);assert match and int(match[1])>=40
    proof['memory_free_percent']=int(match[1])
    assert not [name for name in os.environ if name.startswith('PROJECT_ONE_') and 'DSN' in name and os.environ[name]],'Refuse inherited SQL fixtures'
    for port in (32310,32311):
        with socket.socket() as listener:listener.bind(('127.0.0.1',port))
    assert not mount.exists()
    bindir=root/'bin';bindir.mkdir(exist_ok=True)
    wrapper='''#!/bin/sh
set -eu
if test "${1-}" = install; then
 shift
 exec /Users/passion./.local/opt/node/bin/pnpm install --offline "$@"
fi
if test "${1-}" = test:browser; then
 shift
 exec /Users/passion./.local/opt/node/bin/pnpm test:browser --workers=1 "$@"
fi
exec /Users/passion./.local/opt/node/bin/pnpm "$@"
'''
    (bindir/'pnpm').write_text(wrapper);(bindir/'pnpm').chmod(0o700)
    env=os.environ.copy()
    env.update(PATH=str(bindir)+':'+str(python.parent)+':'+env['PATH'],GOPROXY='off',GOSUMDB='off',GOTOOLCHAIN='local',GOMAXPROCS='2',GOFLAGS='-p=1',CARGO_BUILD_JOBS='1',CARGO_NET_OFFLINE='true',CARGO_TARGET_DIR='/Users/passion./.local/state/ownsolid/lane-rel/gate-native-410ed356/rust-target',NODE_OPTIONS='--max-old-space-size=4096',OWNSOLID_PLAYWRIGHT_PORT_OFFSET='28000')
    gotmp=root/'go-temp';gotmp.mkdir(mode=0o700,exist_ok=True);env['GOTMPDIR']=str(gotmp.resolve())
    proof['browser_ports']=[32310,32311];proof['browser_workers_per_app']=1
    run_make('check',env)
    run_make('build',env)
    attached=subprocess.check_output(['hdiutil','attach','-nomount','ram://4194304'],text=True,timeout=20).strip()
    assert re.fullmatch(r'/dev/disk[0-9]+',attached),attached
    device=attached
    subprocess.run(['diskutil','erasevolume','HFS+','ownsolid-rel-b967d7c8-tests',device],check=True,timeout=30)
    info=plistlib.loads(subprocess.check_output(['diskutil','info','-plist',device],timeout=15))
    assert info['MountPoint']==str(mount)
    volume_uuid=info['VolumeUUID'];proof['scratch_volume_uuid']=volume_uuid
    scratch=mount/'tmp';scratch.mkdir(mode=0o700)
    assert scratch.resolve()==scratch
    disk=os.statvfs(scratch);assert disk.f_bavail/disk.f_blocks>0.30
    env['TMPDIR']=str(scratch)+'/'
    run_make('test',env)
    proof['passed']=True
except BaseException as error:
    proof['passed']=False;proof['failure']=type(error).__name__+': '+str(error)
    print('FULL_GATE_FAILURE',proof['failure'],flush=True)
finally:
    stop_child()
    if device is not None:
        try:
            info=plistlib.loads(subprocess.check_output(['diskutil','info','-plist',device],timeout=15))
            assert volume_uuid is None or info.get('VolumeUUID')==volume_uuid
            subprocess.run(['hdiutil','detach',device],check=True,timeout=30)
            proof['own_scratch_detached']=True
        except BaseException as error:
            proof['cleanup_failure']=type(error).__name__+': '+str(error);proof['passed']=False
    if not audit_preexisting and audit.exists():
        assert stat.S_ISREG(audit.lstat().st_mode) and audit.stat().st_uid==os.geteuid()
        audit.unlink()
    proof['source_clean_after']=git('rev-parse','HEAD')==source and not git('status','--porcelain')
    if not proof['source_clean_after']:proof['passed']=False
    proof['finished_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat()
    write_proof()
print('FULL_GATE_RESULT',json.dumps(proof),flush=True)
sys.exit(0 if proof.get('passed') else 1)
