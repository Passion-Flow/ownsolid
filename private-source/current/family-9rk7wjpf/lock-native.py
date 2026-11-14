raise SystemExit("LANE_M_OLD_GATE_EPOCH_CLOSED")
import fcntl
import os
import pathlib
import stat
import sys

path = pathlib.Path("/private/tmp/ownsolid-workstation-full-gate.lock")
if path.is_symlink():
    raise SystemExit("A symlink cannot carry the workstation lock")
if path.is_dir():
    raise SystemExit("The workstation directory lock is occupied")
fd = os.open(path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
info = os.fstat(fd)
if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1:
    os.close(fd)
    raise SystemExit("Unexpected workstation lock ownership")
try:
    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
except BlockingIOError:
    os.close(fd)
    raise SystemExit(75)
os.set_inheritable(fd, True)
os.execvp("bash", ["bash", sys.argv[1], sys.argv[2]])
