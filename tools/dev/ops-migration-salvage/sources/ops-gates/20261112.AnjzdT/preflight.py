import os
import re
import shutil
import subprocess

active = []
for line in subprocess.check_output(["ps", "-Ao", "pid=,ucomm=,args="], text=True).splitlines():
    parts = line.strip().split(None, 2)
    if len(parts) < 3:
        continue
    pid, command, args = parts
    if command in {"make", "gmake", "gnumake"} and re.search(r"\b(check|build|test)\b", args):
        active.append((pid, command))
    elif command in {"node", "pnpm", "nodejs"} and re.search(r"playwright.*\btest\b|\bvite\b.*\bbuild\b", args):
        active.append((pid, "browser"))
    elif command in {"go", "cargo", "docker"} and re.search(r"\b(test|build|run)\b", args):
        active.append((pid, command))
load = os.getloadavg()[0]
memory = subprocess.check_output(["memory_pressure", "-Q"], text=True)
free = int(re.search(r"free percentage: (\d+)%", memory).group(1))
disk = shutil.disk_usage(".")
print(f"load={load:.2f} cores={os.cpu_count()} memory_free={free}% disk_free_gib={disk.free/1024**3:.1f} active={active}", flush=True)
if active or load > os.cpu_count()*0.9 or free < 25 or disk.free < 15*1024**3:
    raise SystemExit("Workstation preflight refused")

print("Atomic carrier validation and flock acquisition follow resource checks", flush=True)
