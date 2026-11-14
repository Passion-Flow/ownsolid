#!/usr/bin/env python3
"""Fail-closed resource admission and cgroup samples for the ARC-105 fixture."""
import argparse
import json
import os
import re
import subprocess
from datetime import datetime, timezone
from pathlib import Path

GIB = 1 << 30
LIMITS = {"plane": 8 * GIB, "server": 6 * GIB, "worker": 6 * GIB}
ARM_HEADROOM = {"plane": 2 * GIB, "server": GIB, "worker": GIB}
CGROOT = Path("/sys/fs/cgroup")
DEL_PATH = re.compile(r"(?:/var/tmp/lane-del[-./]|/root/lane-del(?:/|\b))", re.I)
DEL_CONTAINER = re.compile(r"(^|[-_])lane[-_]?del|^del[-_]", re.I)
# Only process identities and roles enter the evidence. Arguments may hold test secrets.
RECOVERY_PROCESS = {"e2e.test", "install.test", "dockerd", "containerd",
                    "syft", "rsync", "unshare", "release-factory", "ownsolid-install"}


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True,
                          timeout=10).stdout.strip()


def active_del():
    active = []
    for path in Path("/proc").iterdir():
        if not path.name.isdecimal():
            continue
        try:
            comm = (path / "comm").read_text().strip()
            args = (path / "cmdline").read_bytes().decode(errors="replace")
            if comm not in RECOVERY_PROCESS and "go test" not in args.replace("\x00", " "):
                continue
            cwd = os.readlink(path / "cwd")
            if DEL_PATH.search(args) or DEL_PATH.search(cwd):
                active.append({"pid": int(path.name), "command": comm})
        except (FileNotFoundError, PermissionError, ProcessLookupError):
            continue
    containers = [name for name in command("docker", "ps", "--format", "{{.Names}}").splitlines()
                  if DEL_CONTAINER.search(name)]
    return {"processes": sorted(active, key=lambda p: p["pid"]), "containers": containers}


def pressures(resource):
    values = {}
    for line in Path(f"/proc/pressure/{resource}").read_text().splitlines():
        kind, *parts = line.split()
        values[kind] = {k: int(v) if k == "total" else float(v)
                        for k, v in (part.split("=", 1) for part in parts)}
    return values


def host(path):
    values = {}
    for line in Path("/proc/meminfo").read_text().splitlines():
        name, value = line.split(":", 1)
        if name in ("MemFree", "MemAvailable", "SwapFree"):
            values[name] = int(value.split()[0]) * 1024
    disk = os.statvfs(path)
    return {"memory": values, "disk_free_bytes": disk.f_bavail * disk.f_frsize,
            "io_pressure": pressures("io"), "memory_pressure": pressures("memory"),
            "cpu_pressure": pressures("cpu"), "del": active_del()}


def numeric(path):
    value = path.read_text().strip()
    return None if value == "max" else int(value)


def counters(path):
    return {key: int(value) for key, value in (line.split() for line in path.read_text().splitlines())}


def memory_chain(path, root=CGROOT):
    if root not in path.parents:
        raise ValueError("container has no dedicated non-root cgroup")
    chain = []
    while path != root:
        current = numeric(path / "memory.current")
        chain.append({"path": "/" + str(path.relative_to(root)),
                      "current": current, "max": numeric(path / "memory.max"),
                      "high": numeric(path / "memory.high"),
                      "events": counters(path / "memory.events")})
        path = path.parent
    return chain


def containers(project):
    groups = {}
    for role, budget in LIMITS.items():
        name = f"{project}-{role}"
        pid, configured = map(int, command("docker", "inspect", "-f",
                                          "{{.State.Pid}} {{.HostConfig.Memory}}", name).split())
        if pid <= 0:
            raise ValueError(f"{role} has no live cgroup")
        relative = Path(f"/proc/{pid}/cgroup").read_text().split("0::/", 1)[1].strip()
        chain = memory_chain(CGROOT / relative)
        limited = [limit - group["current"] for group in chain
                   for limit in (group["max"], group["high"]) if limit is not None]
        if configured != budget or chain[0]["max"] != budget or not limited:
            raise ValueError(f"{role} memory limit does not match the fixture budget")
        groups[role] = {"headroom": min(limited), "chain": chain}
    return groups


def assess(data, stage, baseline=None):
    failures = []
    if data["del"]["processes"] or data["del"]["containers"]:
        failures.append("DEL recovery is active")
    if stage in ("host", "arm"):
        mem = data["memory"]
        available = 22 * GIB if stage == "host" else 8 * GIB
        if mem["MemAvailable"] < available:
            failures.append(f"MemAvailable below {available // GIB} GiB")
        if mem["MemFree"] < 2 * GIB:
            failures.append("MemFree below 2 GiB")
        if data["disk_free_bytes"] < 30 * GIB:
            failures.append("free disk below 30 GiB")
        if data["io_pressure"]["full"]["avg10"] > 5.0:
            failures.append("full I/O PSI avg10 above 5%")
    if stage == "arm":
        for role, minimum in ARM_HEADROOM.items():
            if data["cgroups"][role]["headroom"] < minimum:
                failures.append(f"{role} effective cgroup headroom below {minimum // GIB} GiB")
    if stage == "sample":
        if data["memory"]["MemAvailable"] < 2 * GIB:
            failures.append("MemAvailable below 2 GiB during measurement")
        for role, current in data["cgroups"].items():
            previous = {g["path"]: g["events"] for g in baseline["cgroups"][role]["chain"]}
            for group in current["chain"]:
                before = previous.get(group["path"])
                if before is None:
                    failures.append(f"{role} cgroup path changed during measurement")
                    continue
                for key in ("high", "max", "oom", "oom_kill"):
                    if group["events"].get(key, 0) > before.get(key, 0):
                        failures.append(f"{role} new cgroup memory.{key} event at {group['path']}")
    return failures


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("stage", choices=("host", "arm", "sample"))
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--project")
    parser.add_argument("--baseline", type=Path)
    args = parser.parse_args()
    data = {"stage": args.stage, "at": datetime.now(timezone.utc).isoformat()}
    try:
        data.update(host(args.root))
        if args.stage != "host":
            if not args.project:
                raise ValueError("project is required for cgroup samples")
            data["cgroups"] = containers(args.project)
        baseline = json.loads(args.baseline.read_text()) if args.baseline else None
        if args.stage == "sample" and baseline is None:
            raise ValueError("arm baseline is required for samples")
        data["refusals"] = assess(data, args.stage, baseline)
    except (OSError, KeyError, ValueError, subprocess.SubprocessError) as exc:
        data["refusals"] = [str(exc)]
    print(json.dumps(data, sort_keys=True))
    return 75 if data["refusals"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
