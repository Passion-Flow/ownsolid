# Retired workstation lock epoch; await explicit coordinator activation.
import sys
print("IAM_OLD_LOCK_EPOCH_CLOSED", file=sys.stderr)
raise SystemExit(75)
import errno, fcntl, json, os, pathlib, signal, stat, subprocess, sys, time
lock = '/private/tmp/ownsolid-workstation-full-gate.lock'
if len(sys.argv) < 2:
    raise SystemExit('command required')
try:
    before = os.lstat(lock)
except FileNotFoundError:
    before = None
if before is not None and (not stat.S_ISREG(before.st_mode) or before.st_uid != os.geteuid()):
    print('IAM_LOCK_CARRIER_REFUSED', flush=True)
    raise SystemExit(75)
fd = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
try:
    opened, current = os.fstat(fd), os.lstat(lock)
    if not stat.S_ISREG(opened.st_mode) or opened.st_uid != os.geteuid() or stat.S_ISLNK(current.st_mode) or (opened.st_dev, opened.st_ino) != (current.st_dev, current.st_ino):
        print('IAM_LOCK_CARRIER_REFUSED', flush=True)
        raise SystemExit(75)
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        print('IAM_LOCK_BUSY', flush=True)
        raise SystemExit(75)
    p = subprocess.Popen(sys.argv[1:], start_new_session=True, pass_fds=(fd,))
    state = pathlib.Path(__file__).parent / 'lock-process.json'
    state.write_text(json.dumps({'wrapper_pid':os.getpid(),'child_pid':p.pid,'lock':lock,'started':time.time(),'command':sys.argv[1:]},indent=2)+'\n')
    def stop(signum, frame):
        if p.poll() is None:
            os.killpg(p.pid, signum)
            try:p.wait(timeout=25)
            except subprocess.TimeoutExpired:
                os.killpg(p.pid,signal.SIGKILL);p.wait()
        raise SystemExit(128+signum)
    signal.signal(signal.SIGINT,stop);signal.signal(signal.SIGTERM,stop)
    code=p.wait()
    state.write_text(json.dumps({'wrapper_pid':os.getpid(),'child_pid':p.pid,'lock':lock,'exit_code':code,'finished':time.time()},indent=2)+'\n')
    raise SystemExit(code)
finally:
    os.close(fd)
