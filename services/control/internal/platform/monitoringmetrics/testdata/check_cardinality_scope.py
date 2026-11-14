import datetime as dt
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import sys
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

if len(sys.argv) != 2:
    raise SystemExit("Usage: check_cardinality_scope.py /absolute/path/to/verified-victoria-metrics")
binary = Path(sys.argv[1])
if not binary.is_absolute() or not binary.is_file() or not os.access(binary, os.X_OK):
    raise SystemExit("Require an absolute path to a verified VictoriaMetrics binary")

interfaces = subprocess.check_output(["ip", "-o", "link", "show"], text=True).splitlines()
if len(interfaces) != 1 or ": lo:" not in interfaces[0]:
    raise SystemExit("Require a loopback-only network namespace")
for family in ("-4", "-6"):
    if subprocess.check_output(["ip", family, "route", "show", "default"]).strip():
        raise SystemExit("Require a network namespace without default routes")
subprocess.run(["ss", "-lntp"], check=True)

client = build_opener(ProxyHandler({}))
stamp = int(time.time()) - 20
day = dt.datetime.fromtimestamp(stamp, tz=dt.timezone.utc).date().isoformat()
def row(name, tenant, workspace, agent):
    labels = [f'workspace_id="{workspace}"', f'agent_id="{agent}"']
    if tenant:
        labels.append(f'tenant_id="{tenant}"')
    return f'{name}{{{",".join(labels)}}} 1 {stamp * 1000}\n'
lines = [row("scope_a_cpu", "tenant-a", "work-a", f"agent-{i:05d}") for i in range(10_000)]
lines += [
    row("scope_a_queue", "tenant-a", "work-a", "agent-00000"),
    row("scope_a_cpu", "tenant-b", "work-a", "foreign-agent"),
    row("scope_b_only", "tenant-b", "work-a", "foreign-agent"),
    row("scope_w_only", "tenant-a", "work-b", "foreign-work-agent"),
    row("scope_unowned", "", "work-a", "unowned-agent"),
]
data = "".join(lines).encode()
with tempfile.TemporaryDirectory(prefix="ownsolid-cardinality-") as work, open(work+"/service.log", "w+") as log:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        addr = "127.0.0.1:" + str(sock.getsockname()[1])
    base = "http://" + addr
    service = subprocess.Popen([str(binary),
        "-httpListenAddr="+addr, "-storageDataPath="+work+"/data",
        "-memory.allowedBytes=128MB", "-search.latencyOffset=0s",
        "-search.maxTSDBStatusSeries=20000", "-search.maxStatusRequestDuration=3s"], stdout=log, stderr=log)
    def request(path, query=(), payload=None):
        headers = {"Content-Type": "text/plain"} if payload is not None else {}
        with client.open(Request(base+path+"?"+urlencode(query), data=payload, headers=headers), timeout=6) as response:
            limit = 256 * 1024
            result = response.read(limit + 1)
            assert len(result) <= limit, "fixture response exceeded its budget"
            return result
    def stats(scope=True, match="", top=3):
        query = [("date", day), ("topN", str(top)), ("focusLabel", "agent_id")]
        if scope:
            query.extend([("extra_label", "tenant_id=tenant-a"), ("extra_label", "workspace_id=work-a")])
        if match:
            query.append(("match[]", match))
        reply = json.loads(request("/api/v1/status/tsdb", query))
        assert reply["status"] == "success", reply
        return reply["data"]
    def ranked(view, field):
        return {entry["name"]: entry["value"] for entry in view.get(field, [])}
    try:
        deadline = time.monotonic() + 25
        while service.poll() is None and time.monotonic() < deadline:
            try:
                request("/health")
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("VictoriaMetrics did not start")
        request("/api/v1/import/prometheus", payload=data)
        request("/internal/force_flush")
        for _ in range(30):
            view = stats()
            metric = ranked(view, "seriesCountByMetricName")
            if metric == {"scope_a_cpu": 10_000, "scope_a_queue": 1}:
                break
            time.sleep(0.1)
        baseline = stats(scope=False)
        assert baseline["totalSeries"] == len(lines) and view["totalSeries"] == 10_001
        agent = ranked(view, "seriesCountByFocusLabelValue")
        assert metric == {"scope_a_cpu": 10_000, "scope_a_queue": 1}, metric
        assert len(agent) == 3 and agent["agent-00000"] == 2 and sorted(agent.values()) == [1,1,2], agent
        assert all(a.startswith("agent-") for a in agent), agent
        for match in ('{tenant_id="tenant-b"}', '{workspace_id="work-b"}', '{tenant_id=""}'):
            conflicted = stats(match=match)
            assert not ranked(conflicted, "seriesCountByMetricName") and not ranked(conflicted, "seriesCountByFocusLabelValue"), conflicted
        assert len(ranked(stats(top=1), "seriesCountByFocusLabelValue")) == 1
        before = next(row for row in view["seriesCountByMetricName"] if row["name"] == "scope_a_cpu")
        foreign = json.loads(request("/api/v1/query", [("query", 'scope_a_cpu{tenant_id="tenant-b"}'), ("time", stamp+5)]))
        assert len(foreign["data"]["result"]) == 1, foreign
        after = next(row for row in stats()["seriesCountByMetricName"] if row["name"] == "scope_a_cpu")
        times = []
        for _ in range(20):
            started = time.monotonic()
            observed = stats()
            times.append((time.monotonic()-started)*1000)
            assert ranked(observed, "seriesCountByMetricName") == metric
        times.sort()
        assert after["requestsCount"] > before["requestsCount"], (before, after)
        report = dict(address=addr, totalRows=len(lines), inputBytes=len(data),
            p50Ms=round(times[9], 2), p95Ms=round(times[18], 2), maxMs=round(times[19], 2),
            scopedMetrics=metric, agents=agent, scopedTotal=view.get("totalSeries"),
            globalTotal=baseline.get("totalSeries"), metadataBefore=before, metadataAfter=after)
    except Exception:
        log.seek(0)
        print(log.read()[-3200:])
        raise
    finally:
        service.terminate()
        try:
            service.wait(timeout=12)
        except subprocess.TimeoutExpired:
            service.kill()
            service.wait()
            raise
    assert service.returncode == 0, "VictoriaMetrics failed to stop cleanly"
    report["storedBytesAfterShutdown"] = sum(f.stat().st_size for f in Path(work+"/data").rglob("*") if f.is_file())
    print(json.dumps(report))
