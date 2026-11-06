package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The timeline and thresholds of docs/research/fleet-load-test.md. A threshold changes only
// together with that note.
const (
	loadAgents         = 2000
	loadHeartbeat      = 5
	loadSeconds        = 1500
	loadSteadyFrom     = 60
	loadSteadyTo       = 660
	loadBatchEvery     = 60
	loadLongTasksAt    = 960
	loadLongTaskWindow = 30
	loadLongTaskLength = 90
	loadRestartAt      = 990
	// loadStreamBy is threshold 7: three minutes after the restart, past its long tasks.
	loadStreamBy = 1170

	// loadHeartbeatP99 and loadDispatchP99 are thresholds 2 and 3; loadReconnect is 4.
	loadHeartbeatP99 = 1000.0
	loadDispatchP99  = 5.0
	loadReconnect    = 300 * time.Second
)

var loadFaults = []map[string]any{
	{"kind": "disconnect", "at_seconds": 720, "percent": 100},
	{"kind": "partition", "at_seconds": 780, "percent": 30, "duration_seconds": 60},
	{"kind": "long_tasks", "at_seconds": loadLongTasksAt, "percent": 100, "duration_seconds": loadLongTaskWindow},
}

// databaseSample is what PostgreSQL has counted so far.
type databaseSample struct {
	at                                  time.Time
	commits, inserted, updated, deleted int64
	walBytes                            int64
}

func (e environment) sampleDatabase(t *testing.T) databaseSample {
	t.Helper()
	row := e.sql(t, "SELECT xact_commit||' '||tup_inserted||' '||tup_updated||' '||tup_deleted||' '||(pg_current_wal_lsn()-'0/0')::bigint FROM pg_stat_database WHERE datname=current_database()")
	sample := databaseSample{at: time.Now()}
	if _, err := fmt.Sscan(row, &sample.commits, &sample.inserted, &sample.updated, &sample.deleted, &sample.walBytes); err != nil {
		t.Fatalf("database sample %q: %v", row, err)
	}
	return sample
}

// logRates records what the database did between two samples.
func logRates(t *testing.T, name string, from, to databaseSample) {
	t.Helper()
	seconds := to.at.Sub(from.at).Seconds()
	t.Logf("%s, per second over %.0f s: %.0f commits, %.0f rows inserted, %.0f updated, %.0f deleted, %.0f KB of WAL", name, seconds,
		float64(to.commits-from.commits)/seconds, float64(to.inserted-from.inserted)/seconds, float64(to.updated-from.updated)/seconds,
		float64(to.deleted-from.deleted)/seconds, float64(to.walBytes-from.walBytes)/seconds/1024)
}

// logResources records what the containers use now.
func logResources(t *testing.T, when string) {
	t.Helper()
	output, err := run(t, "docker", "stats", "--no-stream", "--format", "{{.Name}} cpu={{.CPUPerc}} memory={{.MemUsage}}")
	if err != nil {
		t.Fatalf("docker stats: %v %s", err, output)
	}
	t.Logf("%s:\n%s", when, output)
}

// loadSampleEvery is how often the run samples what control and PostgreSQL wait on.
const loadSampleEvery = 5 * time.Second

// sampleLoad records, every loadSampleEvery until stop closes, what PostgreSQL's active
// sessions wait on, its checkpoints, and the CPU use and CFS throttling of the control and
// PostgreSQL containers, so a missed threshold can be tied to its cause. Lines are relative
// to started. It runs off the test goroutine and so never fails the test.
func (e environment) sampleLoad(started time.Time, stop <-chan struct{}) <-chan []string {
	out := make(chan []string, 1)
	go func() {
		var lines []string
		previous := map[string][2]int64{}
		ticker := time.NewTicker(loadSampleEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				out <- lines
				return
			case <-ticker.C:
			}
			line := fmt.Sprintf("t=%d", int(time.Since(started).Seconds()))
			query := "SELECT coalesce(string_agg(w||'='||n, ' '),'none') FROM (SELECT coalesce(wait_event_type||':'||wait_event,'cpu') w, count(*) n FROM pg_stat_activity WHERE state='active' AND pid<>pg_backend_pid() GROUP BY 1) a"
			query += " UNION ALL SELECT 'checkpoints='||num_timed||'+'||num_requested||' write_ms='||round(write_time) FROM pg_stat_checkpointer"
			query += " UNION ALL SELECT 'autovacuum='||count(*) FROM pg_stat_activity WHERE backend_type='autovacuum worker'"
			if output, err := exec.Command("docker", "compose", "--env-file", e.root+"/compose.env", "-f", e.root+"/compose.yaml", "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", query).Output(); err == nil {
				line += " pg[" + strings.Join(strings.Fields(string(output)), " ") + "]"
			}
			for _, container := range []string{"ownsolid-control-1", "ownsolid-postgres-1"} {
				raw, err := exec.Command("docker", "exec", container, "cat", "/sys/fs/cgroup/cpu.stat").Output()
				if err != nil {
					continue
				}
				var usage, throttled int64
				for _, field := range strings.Split(string(raw), "\n") {
					name, value, _ := strings.Cut(field, " ")
					n, _ := strconv.ParseInt(value, 10, 64)
					switch name {
					case "usage_usec":
						usage = n
					case "throttled_usec":
						throttled = n
					}
				}
				last, seen := previous[container]
				previous[container] = [2]int64{usage, throttled}
				if seen {
					line += fmt.Sprintf(" %s cpu=%.2f throttled_ms=%d", strings.TrimSuffix(strings.TrimPrefix(container, "ownsolid-"), "-1"),
						float64(usage-last[0])/float64(loadSampleEvery.Microseconds()), (throttled-last[1])/1000)
				}
			}
			lines = append(lines, line)
		}
	}()
	return out
}

// TestFleetLoad is the P0 load test: 2,000 simulated nodes at twice the planned load, a
// reconnect of the whole fleet, and a control restart while work runs. It judges the
// thresholds of docs/research/fleet-load-test.md and takes about half an hour.
func TestFleetLoad(t *testing.T) {
	if os.Getenv("OWNSOLID_E2E_LOAD") == "" {
		t.Skip("needs OWNSOLID_E2E_LOAD, and OWNSOLID_E2E_RUN=TestFleetLoad in run-single-node.sh")
	}
	e := loadEnvironment(t)
	if e.simulator == "" {
		t.Fatal("OWNSOLID_E2E_SIMULATOR names no simulator binary")
	}
	directory := t.TempDir()
	e.apply(t, e.configurationWith(t, directory, "single-node", e.controlImage, fleetLimits))
	admin, csrf := e.adminSession(t, e.password(t))
	e.installLicense(t, admin, csrf)
	e.setPacing(t, admin, csrf, loadHeartbeat, 10, 1, 60, "Fleet load test at twice the planned rate")
	fleet := e.enrollFleet(t, admin, csrf, loadAgents, map[string]any{
		"duration_seconds": loadSeconds, "faults": loadFaults, "long_task_seconds": loadLongTaskLength, "window_seconds": 60,
	})
	targets := fleet.targets()
	fleet.start(t, time.Duration(loadSteadyFrom)*time.Second)
	stopSampling := make(chan struct{})
	samples := e.sampleLoad(fleet.started, stopSampling)

	fleet.at(loadSteadyFrom)
	before := e.sampleDatabase(t)
	logResources(t, "at the start of the steady stretch")
	var dispatched []string
	for at := loadSteadyFrom + 30; at < loadSteadyTo; at += loadBatchEvery {
		fleet.at(at)
		// A new CSRF token per batch keeps the writes inside its lifetime.
		ids := createTasks(t, admin, admin.csrfToken(t), targets)
		e.waitForTasks(t, fmt.Sprintf("batch at %d s", at), ids, 30*time.Second)
		dispatched = append(dispatched, ids...)
	}
	fleet.at(loadSteadyTo)
	logRates(t, "steady stretch", before, e.sampleDatabase(t))
	logResources(t, "at the end of the steady stretch")

	list := "'" + strings.Join(dispatched, "','") + "'"
	row := e.sql(t, "SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY s)||' '||percentile_cont(0.99) WITHIN GROUP (ORDER BY s)||' '||max(s) FROM (SELECT extract(epoch FROM completed_at-created_at) s FROM node_tasks WHERE id IN ("+list+")) d")
	var median, p99, slowest float64
	if _, err := fmt.Sscan(row, &median, &p99, &slowest); err != nil {
		t.Fatalf("dispatch %q: %v", row, err)
	}
	t.Logf("dispatch of %d tasks: median %.3f s, p99 %.3f s, slowest %.3f s", len(dispatched), median, p99, slowest)

	fleet.at(loadLongTasksAt + 2)
	long := createTasks(t, admin, admin.csrfToken(t), targets)
	fleet.at(loadRestartAt)
	running := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ('"+strings.Join(long, "','")+"') AND state='running'")
	if running != strconv.Itoa(len(long)) {
		t.Fatalf("%s of %d long tasks are running before the restart", running, len(long))
	}
	restarted := time.Now()
	if output, err := e.compose(t, "restart", "control"); err != nil {
		t.Fatalf("restart control: %v %s", err, output)
	}
	t.Logf("control restarted in %s", time.Since(restarted).Round(100*time.Millisecond))
	var reconnected time.Duration
	// Nodes running a long task report only through renewals until it ends.
	idle := strings.Join(idleNodes(fleet, targets), "','")
	for {
		online := e.sql(t, "SELECT count(*) FROM managed_nodes WHERE id IN ('"+idle+"') AND last_heartbeat_at > '"+restarted.UTC().Format(time.RFC3339Nano)+"'")
		reconnected = time.Since(restarted)
		if online == strconv.Itoa(loadAgents-len(targets)) {
			break
		}
		if reconnected > loadReconnect {
			t.Fatalf("threshold 4: %s of %d idle agents back %s after the restart", online, loadAgents-len(targets), reconnected.Round(time.Second))
		}
		time.Sleep(time.Second)
	}
	t.Logf("every idle agent heard from %s after the restart began", reconnected.Round(100*time.Millisecond))

	e.waitForTasks(t, "tasks running through the restart", long, 150*time.Second)
	firstAttempt := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ('"+strings.Join(long, "','")+"') AND attempt=1 AND acknowledged_attempt=1")
	finishedAt := e.sql(t, "SELECT round(extract(epoch FROM max(completed_at)-min(created_at))::numeric,1) FROM node_tasks WHERE id IN ('"+strings.Join(long, "','")+"')")
	t.Logf("long tasks: %s on their first attempt, %s s from the first created to the last result", firstAttempt, finishedAt)

	fleet.wait(t, loadSeconds, time.Minute)
	close(stopSampling)
	t.Logf("samples every %s:\n%s", loadSampleEvery, strings.Join(<-samples, "\n"))
	if output, err := e.compose(t, "logs", "--no-color", "postgres"); err == nil {
		var checkpoints []string
		for _, line := range strings.Split(output, "\n") {
			if strings.Contains(line, "checkpoint") || strings.Contains(line, "automatic vacuum") {
				checkpoints = append(checkpoints, line)
			}
		}
		t.Logf("PostgreSQL checkpoints and vacuums:\n%s", strings.Join(checkpoints, "\n"))
	}
	final := e.sampleDatabase(t)
	logRates(t, "whole run", before, final)
	logResources(t, "at the end of the run")
	t.Logf("simulator process: %s user, %s system, %d MB at its largest", fleet.usage.UserTime().Round(time.Second), fleet.usage.SystemTime().Round(time.Second), fleet.usage.SysUsage().(*syscall.Rusage).Maxrss/1024)
	t.Logf("largest tables:\n%s", e.sql(t, "SELECT relname||' '||pg_size_pretty(pg_total_relation_size(c.oid)) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE relkind='r' AND nspname NOT IN ('pg_catalog','information_schema') ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 8"))
	out := fleet.report(t)

	// Every failure is collected, so one run reports every threshold it missed.
	for _, w := range out.Windows {
		if w.EndSeconds <= loadSteadyFrom || w.EndSeconds > loadSteadyTo {
			continue
		}
		heartbeat, claim := w.Calls["heartbeat"], w.Calls["claim"]
		if heartbeat.Failed != 0 || claim.Failed != 0 || w.Connected != loadAgents {
			t.Errorf("threshold 1, window ending at %d s: %d heartbeats and %d claims failed, %d connected", w.EndSeconds, heartbeat.Failed, claim.Failed, w.Connected)
		}
		if heartbeat.P99 > loadHeartbeatP99 {
			t.Errorf("threshold 2, window ending at %d s: heartbeat p99 %.0f ms", w.EndSeconds, heartbeat.P99)
		}
	}
	if p99 > loadDispatchP99 {
		t.Errorf("threshold 3: dispatch p99 %.3f s", p99)
	}
	for _, f := range out.Faults {
		if f.Measured && (f.FullRecovery == nil || *f.FullRecovery > loadReconnect.Seconds()) {
			t.Errorf("threshold 4: %s recovered %d of %d agents, full recovery %v", f.Kind, f.Recovered, f.Agents, f.FullRecovery)
		}
	}
	if firstAttempt != strconv.Itoa(len(long)) {
		t.Errorf("threshold 5: %s of %d tasks finished on their first attempt", firstAttempt, len(long))
	}
	if out.Connected != loadAgents || out.Revoked != 0 {
		t.Errorf("at the end %d of %d agents connected, %d revoked", out.Connected, loadAgents, out.Revoked)
	}
	for _, w := range out.Windows {
		if w.EndSeconds >= loadStreamBy && w.OnStream != loadAgents {
			t.Errorf("threshold 7, window ending at %d s: %d of %d agents on the stream", w.EndSeconds, w.OnStream, loadAgents)
		}
	}
	inspected, err := run(t, "docker", "inspect", "--format", "{{.RestartCount}} {{.State.OOMKilled}}", "ownsolid-control-1")
	if err != nil || strings.TrimSpace(inspected) != "0 false" {
		t.Errorf("threshold 6: control restarts and out-of-memory kill %q: %v", strings.TrimSpace(inspected), err)
	}
	output, err := e.compose(t, "logs", "control")
	if err != nil {
		t.Fatal(err)
	}
	var refusals []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "agent_call_refused") {
			refusals = append(refusals, line)
		}
	}
	t.Logf("control's refusals, at most one line every ten seconds:\n%s", strings.Join(refusals, "\n"))
	for _, failure := range []string{"node_task_wakes_read_failed", "node_task_wake_trim_failed", "node_liveness_flush_failed", "node_task_sweep_failed"} {
		if n := strings.Count(output, failure); n > 0 {
			t.Errorf("threshold 6: control logged %s %d times", failure, n)
		}
	}
	summary, _ := json.Marshal(map[string]any{"agents": loadAgents, "heartbeat_seconds": loadHeartbeat, "dispatch_p99_seconds": p99, "restart_reconnect_seconds": reconnected.Seconds()})
	t.Logf("summary: %s", summary)
}

// idleNodes is the fleet without busy, the nodes a long task keeps from heartbeating.
func idleNodes(f *simulatedRun, busy []string) []string {
	skip := map[string]bool{}
	for _, id := range busy {
		skip[id] = true
	}
	var out []string
	for _, identity := range f.nodes {
		if !skip[identity.NodeID] {
			out = append(out, identity.NodeID)
		}
	}
	return out
}
