
# OWNSOLID_OLD_MAC_EPOCH_CLOSED
import sys as _ownsolid_closed_epoch_sys
_ownsolid_closed_epoch_sys.stderr.write("Old Mac admission epoch is closed; await coordinated activation.\n")
raise SystemExit(78)
import fcntl
import os
import stat
import subprocess
import sys

if len(sys.argv) < 2:
    raise SystemExit('Provide the foreground command for the shared Mac window')

path = '/private/tmp/ownsolid-workstation-full-gate.lock'
try:
    fd = os.open(path, os.O_RDWR | os.O_NOFOLLOW)
except OSError as error:
    raise SystemExit(f'Shared Mac lock carrier unavailable; no command started: {error}')

with os.fdopen(fd, 'r+b') as lock:
    info = os.fstat(lock.fileno())
    if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid():
        raise SystemExit('Shared Mac lock must be a regular file owned by this user')
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise SystemExit('Shared Mac window occupied; no command started')
    result = subprocess.run(sys.argv[1:])
    raise SystemExit(result.returncode)
