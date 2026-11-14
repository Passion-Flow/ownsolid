"""Private lifecycle decision prototype. No lock, child process, or job entry."""
import ctypes
import errno


class UnprovenExit(RuntimeError):
    """The saved evidence cannot distinguish exit from an unreadable live task."""


class UsageV0(ctypes.Structure):
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [
        (name, ctypes.c_uint64)
        for name in (
            "user_time", "system_time", "idle_wakeups", "interrupt_wakeups",
            "pageins", "wired_size", "resident_size", "phys_footprint",
            "start_abstime", "exit_abstime",
        )
    ]


def read_usage(pid):
    """Read one documented RUSAGE_INFO_V0 result, retaining failure errno."""
    if ctypes.sizeof(UsageV0) != 96:
        raise RuntimeError("Unsupported local rusage ABI")
    lib = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
    lib.proc_pid_rusage.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_void_p]
    lib.proc_pid_rusage.restype = ctypes.c_int
    value = UsageV0()
    ctypes.set_errno(0)
    code = lib.proc_pid_rusage(pid, 0, ctypes.byref(value))
    result = {"return": code, "errno": ctypes.get_errno()}
    if code == 0:
        result.update(startAbstime=value.start_abstime,
                      exitAbstime=value.exit_abstime,
                      residentBytes=value.resident_size)
    return result


def resolve_missing(known, *, usage=None, bsd=None, signal_errno=None):
    """Return a retirement event only; never synthesize an RSS observation.

    Before using this prototype, initial ownership must bind BSD birth/UID and
    usage start on a live task with BSD reads on both sides. Unproven or changed
    identity requires rejection or independently proven new ancestry.
    """
    if bsd is not None:
        if (bsd["pid"] != known["pid"] or bsd["uid"] != known["uid"]
                or bsd["ruid"] != known["uid"]
                or bsd["startTime"] != known["startTime"]):
            raise UnprovenExit("BSD identity changed; do not adopt replacement")
        if bsd["status"] == 5:
            return {"pid": known["pid"], "proof": "same-lifetime-kernel-zombie"}
    if usage is not None and usage.get("return") == 0:
        expected = known.get("startAbstime")
        observed = usage.get("startAbstime")
        if expected is None or observed != expected:
            raise UnprovenExit("Unbound or changed kernel lifetime")
        end = usage.get("exitAbstime", 0)
        if end >= expected > 0:
            return {"pid": known["pid"], "proof": "same-lifetime-kernel-exit",
                    "startAbstime": expected, "exitAbstime": end}
    if signal_errno == errno.ESRCH:
        return {"pid": known["pid"], "proof": "pid-absent-at-kill-zero"}
    raise UnprovenExit("Missing task RSS without conclusive lifecycle evidence")
