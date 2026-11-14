import os
import signal
import subprocess
import sys

action = sys.argv[1]
if action not in {"fetch", "push"}:
    raise SystemExit("Expected fetch or push")
suffix = ["fetch", "origin"] if action == "fetch" else ["push", "origin", "HEAD:main"]
routes = [
    ["-c", "http.proxy=http://127.0.0.1:1082"],
    [],
]
for route in routes:
    command = ["git", "-c", "http.lowSpeedLimit=1", "-c", "http.lowSpeedTime=20", *route, *suffix]
    process = subprocess.Popen(command, start_new_session=True)
    try:
        code = process.wait(timeout=45)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        print(f"{action} timed out on the selected route", flush=True)
        continue
    if code == 0:
        raise SystemExit(0)
    print(f"{action} exited {code} on the selected route", flush=True)
raise SystemExit(1)
