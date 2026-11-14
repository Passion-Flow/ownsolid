"""Check the disposable diagnosis outcomes without reading credentials."""
from collections import Counter, defaultdict
import json
from pathlib import Path
import re
import sys

root=Path(sys.argv[1])
groups=defaultdict(list)
for line in (root/"test.log").read_text().splitlines():
    if line.startswith("{"):
        item=json.loads(line)
        if item.get("request_id"):
            groups[item["request_id"]].append(item)

pg=(root/"postgres.log").read_text()
observed=Counter()
expected={
    "healthy":200, "lock_timeout_before_commit":503, "deadline_before_commit":503,
    "connection_loss_before_commit_send":409, "lost_successful_commit_reply":409,
    "cancel_during_commit_call":409, "deadline_during_commit_call":409,
}
def exactly(rows, event, phase=None):
    matches=[r for r in rows if r["event"]==event and (phase is None or r.get("phase")==phase)]
    assert len(matches)==1,(event,phase,len(matches))
    return matches[0]

for request,rows in groups.items():
    member=exactly(rows,"sql_start","member_select")
    pid=member["backend_pid"]
    assert pid>0 and isinstance(member["remaining_ms"],int)
    assert exactly(rows,"pool_acquire_end")["backend_pid"]==pid
    assert exactly(rows,"request_start")["remaining_ms"]>0
    if any(r["event"]=="process_exit" for r in rows):
        end=exactly(rows,"process_exit")
        assert end["shell_exit"]==137 and end["signal"]=="SIGKILL"
        assert exactly(rows,"client_transport_error")["http_status"] is None
        assert not any(r["event"]=="request_end" or r.get("phase")=="commit" for r in rows)
        assert re.search(rf"\[{pid}\].*arc104-exit-child.*disconnection",pg)
        observed["process_exit"]+=1
        continue
    case=exactly(rows,"fault_case")["case"]
    end=exactly(rows,"request_end")
    assert end["http_status"]==expected[case]
    query=exactly(rows,"sql_end","member_select")
    assert query["backend_pid"]==pid
    if end["http_status"]==503:
        assert not any(r.get("phase")=="commit" for r in rows)
        if case.startswith("lock_"):
            assert query["sqlstate"]=="55P03"
            assert re.search(rf"\[{pid}\].*55P03",pg)
        else:
            assert query["error_kind"]=="deadline" and query["remaining_ms"]==0
    else:
        commit=exactly(rows,"sql_end","commit")
        assert commit["backend_pid"]==pid
        assert commit["commit_acknowledged"]==(case=="healthy")
        if case=="lost_successful_commit_reply":
            wire=exactly(rows,"wire_commit")
            assert wire["server_ack"] and wire["forwarded"] and wire["suppressed"] and wire["commit_count"]==1
        if case=="connection_loss_before_commit_send":
            wire=exactly(rows,"wire_commit")
            assert not wire["server_ack"] and not wire["forwarded"] and wire["suppressed"] and wire["commit_count"]==1
        if case=="deadline_during_commit_call":
            assert commit["error_kind"]=="deadline" and commit["remaining_ms"]==0
    observed[case]+=1

assert observed==Counter({k:3 for k in (*expected,"process_exit")}),observed
events=(root/"docker-observer/events.log").read_text().splitlines()
assert all(row.split()[1] in {"create","start","die","oom","restart","stop","kill"} for row in events), events
assert any(" die " in row and row.rstrip().endswith("postgres 0") for row in events), events
assert any(" die " in row and row.rstrip().endswith("control 37") for row in events), events
assert any(path.stat().st_size for path in (root/"docker-observer").glob("postgres-*.final.log"))
assert any("observer-control-sentinel" in path.read_text() for path in (root/"docker-observer").glob("control-*.final.log"))
print("Correlated",sum(observed.values()),"requests with SQL connection and deadline evidence")
print("PostgreSQL exit 0, Control-labelled collector sentinel exit 37, and blocked member child exit 137 verified")
