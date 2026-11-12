import datetime as dt
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import sys
import uuid
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import ProxyHandler, Request, build_opener

if len(sys.argv) != 2:
    raise SystemExit("Usage: check_cardinality_scope.py /absolute/path/to/verified-victoria-metrics")
if not __debug__:
    raise SystemExit("Run the native assertions without Python optimization")
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
def row(name, tenant, workspace, node):
    labels = [f'workspace_id="{workspace}"', f'node_id="{node}"']
    if tenant:
        labels.append(f'tenant_id="{tenant}"')
    return f'{name}{{{",".join(labels)}}} 1 {stamp * 1000}\n'
node_ids = [str(uuid.uuid5(uuid.NAMESPACE_OID, f"ownsolid-cardinality-node-{i}")) for i in range(10000)]
node_ids_set = set(node_ids)
lines = [row("scope_a_cpu", "tenant-a", "work-a", node) for node in node_ids]
lines += [
    row("scope_a_queue", "tenant-a", "work-a", node_ids[0]),
    row("scope_a_cpu", "tenant-b", "work-a", "foreign-node"),
    row("scope_b_only", "tenant-b", "work-a", "foreign-node"),
    row("scope_w_only", "tenant-a", "work-b", "foreign-work-node"),
    row("scope_unowned", "", "work-a", "unowned-node"),
]
data = "".join(lines).encode()

def start_store(binary, work, log, max_series, max_ranks):
    subprocess.run(["ss", "-lntp"], check=True)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        address = "127.0.0.1:" + str(sock.getsockname()[1])
    process = subprocess.Popen([str(binary),
        "-httpListenAddr="+address, "-storageDataPath="+work+"/data",
        "-memory.allowedBytes=128MB", "-search.latencyOffset=0s",
        "-search.maxTSDBStatusSeries="+str(max_series),
        "-search.maxTSDBStatusTopNSeries="+str(max_ranks),
        "-search.maxStatusRequestDuration=3s"], stdout=log, stderr=log)
    return address, process


def wait_store(process, request):
    deadline = time.monotonic() + 25
    while process.poll() is None and time.monotonic() < deadline:
        try:
            request("/health")
            return
        except HTTPError as error:
            error.close()
        except OSError:
            pass
        time.sleep(0.1)
    raise RuntimeError("VictoriaMetrics did not start")


def stop_store(process):
    process.terminate()
    try:
        process.wait(timeout=12)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
        raise
    assert process.returncode == 0, "VictoriaMetrics failed to stop cleanly"


with tempfile.TemporaryDirectory(prefix="ownsolid-cardinality-") as work, open(work+"/service.log", "w+") as log:
    addr, service = start_store(binary, work, log, 20000, 1000)
    base = "http://" + addr
    def request(path, query=(), payload=None):
        headers = {"Content-Type": "text/plain"} if payload is not None else {}
        with client.open(Request(base+path+"?"+urlencode(query), data=payload, headers=headers), timeout=6) as response:
            limit = 256 * 1024
            result = response.read(limit + 1)
            assert len(result) <= limit, "fixture response exceeded its budget"
            return result
    def stats(scope=True, match="", top=3):
        query = [("date", day), ("topN", str(top)), ("focusLabel", "node_id")]
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
        wait_store(service, request)
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
        node = ranked(view, "seriesCountByFocusLabelValue")
        assert metric == {"scope_a_cpu": 10_000, "scope_a_queue": 1}, metric
        assert len(node) == 3 and node[node_ids[0]] == 2 and sorted(node.values()) == [1,1,2], node
        assert all(a in node_ids_set for a in node), node
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
            scopedMetrics=metric, nodes=node, scopedTotal=view.get("totalSeries"),
            globalTotal=baseline.get("totalSeries"), metadataBefore=before, metadataAfter=after)
    except Exception:
        log.seek(0)
        print(log.read()[-3200:])
        raise
    finally:
        stop_store(service)
    addr, service = start_store(binary, work, log, 10000, 2)
    base = "http://" + addr
    report["limitedAddress"] = addr
    try:
        wait_store(service, request)
        try:
            stats(top=1)
        except HTTPError as error:
            with error:
                body = error.read(256 * 1024 + 1)
            assert len(body) <= 256 * 1024
            refused = json.loads(body)
            assert refused.get("status") == "error" and "data" not in refused, refused
            assert error.code == 422 and "number of matching timeseries exceeds 10000" in refused.get("error", ""), refused
            report["scanRefusalStatus"] = error.code
        else:
            raise AssertionError("topN=1 bypassed the native scan limit")
        limited = stats(match="scope_a_cpu", top=99)
        assert limited["totalSeries"] == 10000, limited
        assert ranked(limited, "seriesCountByMetricName") == {"scope_a_cpu":10000}, limited
        assert len(ranked(limited, "seriesCountByFocusLabelValue")) == 2, limited
        recovered = stats(match="scope_a_queue", top=1)
        assert recovered["totalSeries"] == 1, recovered
        assert ranked(recovered, "seriesCountByFocusLabelValue") == {node_ids[0]:1}, recovered
        report["limitedMetricTotal"] = limited["totalSeries"]
        report["nativeRankCap"] = 2
        report["narrowedRetryTotal"] = recovered["totalSeries"]
    except Exception:
        log.seek(0)
        print(log.read()[-3200:])
        raise
    finally:
        stop_store(service)
    report["storedBytesAfterShutdown"] = sum(f.stat().st_size for f in Path(work+"/data").rglob("*") if f.is_file())
    print(json.dumps(report))
