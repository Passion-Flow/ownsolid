# Lane D Mac gate wrapper

Use with_mac_window.py before this lane's next Mac client build, whole-repository
gate or main publication:

    python3 with_mac_window.py -- FOREGROUND_COMMAND ARGUMENT...

The wrapper uses the permanent /private/tmp/ownsolid-workstation-full-gate.lock
regular file, checks ownership and rejects symlinks or directory carriers.
It acquires fcntl.flock(LOCK_EX | LOCK_NB) and keeps the same open file description
for the foreground command. A busy or invalid carrier exits 75 before starting
the command. It never removes, replaces, truncates or writes the carrier.
Do not use it to launch detached services.

OBS has priority for its current client self-check and cross-build window.
This wrapper change does not admit a competing gate or change publication order.
The Linux global/SCI/lane locks remain separate and unchanged.

test_mac_window.py uses only temporary private files. Running it never acquires
the shared carrier and never reruns product tests.

An explicitly assigned short handoff may use --wait-seconds 900,
--execution-seconds 285, --expected-inode 50313339 and --proof PATH. This is one
blocking kernel flock call with a signal deadline, not a polling/regrab queue.
An assigned inode refuses a missing/replaced carrier rather than creating one.
After acquisition the parent passes the same open description and its FD number
in OWNSOLID_MAC_WINDOW_FD; child commands validate and inherit it without taking
another lock. The execution deadline terminates and joins the owned child group
before the parent closes its FD. Default invocations remain nonblocking.
These flags do not grant or imply another resource window.
