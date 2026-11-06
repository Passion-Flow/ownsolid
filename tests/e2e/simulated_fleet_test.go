package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// defaultSimulatedAgents is the fleet the end-to-end run simulates unless
// OWNSOLID_E2E_SIMULATED_AGENTS says otherwise.
const defaultSimulatedAgents = 2000

// simulatedSeconds is how long the simulated fleet runs, and fleetSchedule when each
// scripted fault hits it, in seconds from its start.
const simulatedSeconds = 240

var fleetFaults = []map[string]any{
	{"kind": "disconnect", "at_seconds": 60, "percent": 100},
	{"kind": "partition", "at_seconds": 90, "percent": 30, "duration_seconds": 30},
	{"kind": "lose_replies", "at_seconds": 135, "percent": 25, "duration_seconds": 30},
	{"kind": "lose_cursor", "at_seconds": 175, "percent": 100},
	{"kind": "restart", "at_seconds": 200, "percent": 100},
}

// fleetLimits raises the limits a fleet run from one address meets: every agent holds a
// connection, and all of them share the simulator's address (docs/research/agent-simulator.md).
const fleetLimits = `control_settings:
  runtime:
    http: {max_connections: 4096}
    nodes: {maximum_live_nodes: 10000, maximum_live_join_tokens: 10000}
    rates: {agent_enroll_ip_count: 10000}
    agent_admission:
      address: {per_minute: 60000, burst: 10000}
`

type simulatedIdentity struct {
	NodeID               string `json:"node_id"`
	AcknowledgedSequence uint64 `json:"acknowledged_sequence"`
}

type fleetReport struct {
	Agents    int `json:"agents"`
	Connected int `json:"connected_at_end"`
	OnStream  int `json:"on_stream_at_end"`
	Revoked   int `json:"revoked"`
	Calls     map[string]struct {
		Succeeded int64              `json:"succeeded"`
		Failed    map[string]int64   `json:"failed"`
		Latency   map[string]float64 `json:"latency_milliseconds"`
	} `json:"calls"`
	Tasks struct {
		Succeeded   int64 `json:"succeeded"`
		Resyncs     int64 `json:"resyncs"`
		LostReplies int64 `json:"lost_replies"`
	} `json:"tasks"`
	Windows []struct {
		EndSeconds int `json:"end_seconds"`
		Connected  int `json:"connected"`
		OnStream   int `json:"on_stream"`
		Calls      map[string]struct {
			Succeeded int64   `json:"succeeded"`
			Failed    int64   `json:"failed"`
			P99       float64 `json:"p99_milliseconds"`
		} `json:"calls"`
	} `json:"windows"`
	Faults []struct {
		Kind         string   `json:"kind"`
		Agents       int      `json:"agents"`
		Measured     bool     `json:"measured"`
		Recovered    int      `json:"recovered_agents"`
		FullRecovery *float64 `json:"full_recovery_seconds"`
		P99Recovery  *float64 `json:"p99_recovery_seconds"`
	} `json:"faults"`
}

// simulatedRun is a fleet enrolled through the real API and the simulator process running it.
type simulatedRun struct {
	directory, configFile, binary string
	nodes                         []simulatedIdentity
	log                           *lockedBuffer
	finished                      chan struct{}
	exit                          error
	usage                         *os.ProcessState
	started                       time.Time
}

// enrollFleet mints a join token per agent, writes the simulator's configuration with
// settings added, and enrolls the fleet.
func (e environment) enrollFleet(t *testing.T, admin *browser, csrf string, agents int, settings map[string]any) *simulatedRun {
	t.Helper()
	directory := t.TempDir()
	tokens := e.mintTokens(t, admin, csrf, agents)
	writePrivate(t, filepath.Join(directory, "tokens"), []byte(strings.Join(tokens, "\n")+"\n"))
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(directory, "ca.pem"), authority)
	values := map[string]any{
		"control_origin": adminOrigin, "ca_file": filepath.Join(directory, "ca.pem"), "dial_address": "127.0.0.1:8443",
		"identities_file": filepath.Join(directory, "identities.json"), "join_tokens_file": filepath.Join(directory, "tokens"),
		"agents": agents, "enroll_concurrency": 16, "start_seconds": 10, "report_file": filepath.Join(directory, "report.json"),
	}
	for name, value := range settings {
		values[name] = value
	}
	configuration, _ := json.Marshal(values)
	configFile := filepath.Join(directory, "simulator.json")
	writePrivate(t, configFile, configuration)
	started := time.Now()
	if output, err := run(t, e.simulator, "enroll", "--config", configFile); err != nil {
		t.Fatalf("enroll: %v\n%s", err, output)
	}
	t.Logf("%d simulated agents enrolled in %s", agents, time.Since(started).Round(time.Second))
	f := &simulatedRun{directory: directory, configFile: configFile, binary: e.simulator, log: &lockedBuffer{}, finished: make(chan struct{})}
	f.nodes = f.identities(t)
	if len(f.nodes) != agents {
		t.Fatalf("%d identities for %d agents", len(f.nodes), agents)
	}
	return f
}

func (f *simulatedRun) identities(t *testing.T) []simulatedIdentity {
	t.Helper()
	var file struct {
		Agents []simulatedIdentity `json:"agents"`
	}
	raw, err := os.ReadFile(filepath.Join(f.directory, "identities.json"))
	if err != nil || json.Unmarshal(raw, &file) != nil {
		t.Fatalf("identities: %v", err)
	}
	return file.Agents
}

// targets is every twentieth node, which take the tasks of a run.
func (f *simulatedRun) targets() []string {
	var out []string
	for i := 0; i < len(f.nodes); i += 20 {
		out = append(out, f.nodes[i].NodeID)
	}
	return out
}

// start runs the fleet and waits until every agent connected. stop ends it if the test
// fails first.
func (f *simulatedRun) start(t *testing.T, within time.Duration) {
	t.Helper()
	command := exec.Command(f.binary, "run", "--config", f.configFile)
	command.Stdout, command.Stderr = f.log, f.log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		f.exit = command.Wait()
		f.usage = command.ProcessState
		close(f.finished)
	}()
	f.started = time.Now()
	t.Cleanup(func() {
		select {
		case <-f.finished:
		default:
			command.Process.Signal(syscall.SIGTERM)
			<-f.finished
		}
	})
	connected := fmt.Sprintf("agents=%d connected=%d ", len(f.nodes), len(f.nodes))
	for !strings.Contains(f.log.String(), connected) {
		if time.Since(f.started) > within {
			t.Fatalf("the fleet did not connect:\n%s", f.log.String())
		}
		time.Sleep(time.Second)
	}
	t.Logf("every simulated agent connected %s after the start", time.Since(f.started).Round(time.Second))
}

// at waits until seconds into the run.
func (f *simulatedRun) at(seconds int) {
	time.Sleep(time.Until(f.started.Add(time.Duration(seconds) * time.Second)))
}

// wait waits for the simulator to end by itself, within grace of its last second.
func (f *simulatedRun) wait(t *testing.T, seconds int, grace time.Duration) {
	t.Helper()
	select {
	case <-f.finished:
		if f.exit != nil {
			t.Fatalf("simulator: %v\n%s", f.exit, f.log.String())
		}
	case <-time.After(time.Until(f.started.Add(time.Duration(seconds)*time.Second + grace))):
		t.Fatalf("the simulator did not stop:\n%s", f.log.String())
	}
	if !strings.Contains(f.log.String(), "simulator_finished") {
		t.Fatalf("simulator log:\n%s", f.log.String())
	}
}

// report reads the report the run wrote and logs it.
func (f *simulatedRun) report(t *testing.T) fleetReport {
	t.Helper()
	var out fleetReport
	raw, err := os.ReadFile(filepath.Join(f.directory, "report.json"))
	if err != nil || json.Unmarshal(raw, &out) != nil {
		t.Fatalf("report: %v", err)
	}
	t.Logf("report:\n%s", raw)
	return out
}

// nodeList is the fleet's nodes as an SQL list.
func (f *simulatedRun) nodeList() string {
	ids := make([]string, len(f.nodes))
	for i, identity := range f.nodes {
		ids[i] = identity.NodeID
	}
	return "'" + strings.Join(ids, "','") + "'"
}

// online counts the fleet's nodes whose last heartbeat reached the database within the
// online window.
func (e environment) online(t *testing.T, f *simulatedRun) string {
	t.Helper()
	return e.sql(t, "SELECT count(*) FROM managed_nodes WHERE id IN ("+f.nodeList()+") AND last_heartbeat_at > clock_timestamp() - interval '30 seconds'")
}

// createTasks asks each node to collect its facts.
func createTasks(t *testing.T, admin *browser, csrf string, nodes []string) []string {
	t.Helper()
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		var task struct {
			ID string `json:"id"`
		}
		status, raw := admin.requestWith(t, http.MethodPost, nodesPath+"/"+node+"/tasks", map[string]string{"operation": "collect_facts.v1"}, csrf, map[string]string{"Idempotency-Key": newUUID(t)})
		decode(t, status, http.StatusAccepted, raw, &task)
		ids = append(ids, task.ID)
	}
	return ids
}

// waitForTasks waits until every task succeeded on an acknowledged attempt.
func (e environment) waitForTasks(t *testing.T, name string, ids []string, within time.Duration) {
	t.Helper()
	started := time.Now()
	list := "'" + strings.Join(ids, "','") + "'"
	for {
		done := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+list+") AND state='succeeded' AND attempt>0 AND acknowledged_attempt=attempt")
		if done == strconv.Itoa(len(ids)) {
			break
		}
		if time.Since(started) > within {
			t.Fatalf("%s: %s of %d tasks succeeded within %s\n%s", name, done, len(ids), within,
				e.sql(t, "SELECT string_agg(state||':'||count, ', ') FROM (SELECT state, count(*) FROM node_tasks WHERE id IN ("+list+") GROUP BY state) s"))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("%s: %d tasks succeeded after %s", name, len(ids), time.Since(started).Round(100*time.Millisecond))
}

// simulatedFleet enrolls a simulated fleet with real join tokens, runs it through the
// scripted faults while an administrator runs tasks on part of it, and checks the
// simulator's report against control's own record. It uses the administrator session it
// is given, because the run's sign-ins already come close to the limit per account.
func (e environment) simulatedFleet(t *testing.T, admin *browser) {
	t.Helper()
	agents := defaultSimulatedAgents
	if value := os.Getenv("OWNSOLID_E2E_SIMULATED_AGENTS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 {
			t.Fatalf("OWNSOLID_E2E_SIMULATED_AGENTS=%q", value)
		}
		agents = n
	}
	csrf := admin.csrfToken(t)
	fleet := e.enrollFleet(t, admin, csrf, agents, map[string]any{"duration_seconds": simulatedSeconds, "faults": fleetFaults})
	targets := fleet.targets()
	fleet.start(t, 50*time.Second)

	var tasks []string
	batch := func(name string, within time.Duration) {
		t.Helper()
		ids := createTasks(t, admin, csrf, targets)
		e.waitForTasks(t, name, ids, within)
		tasks = append(tasks, ids...)
	}
	batch("steady fleet", 30*time.Second)
	fleet.at(140)
	// Agents healed from the partition may still be in backoff, for up to the reconnect
	// maximum of the default pacing.
	batch("replies lost", 90*time.Second)
	fleet.at(210)
	batch("after a fleet restart", 30*time.Second)

	fleet.wait(t, simulatedSeconds, time.Minute)
	if online := e.online(t, fleet); online != strconv.Itoa(agents) {
		t.Fatalf("%s of %d simulated nodes are online at the end", online, agents)
	}
	out := fleet.report(t)
	if out.Agents != agents || out.Connected != agents || out.OnStream != agents || out.Revoked != 0 {
		t.Fatalf("at the end %d of %d agents connected, %d on the stream, %d revoked", out.Connected, agents, out.OnStream, out.Revoked)
	}
	if len(out.Faults) != len(fleetFaults) {
		t.Fatalf("%d faults reported", len(out.Faults))
	}
	for _, f := range out.Faults {
		if f.Measured && (f.FullRecovery == nil || f.Recovered != f.Agents) {
			t.Fatalf("%s: %d of %d agents recovered", f.Kind, f.Recovered, f.Agents)
		}
	}
	if out.Tasks.Succeeded < int64(len(tasks)) || out.Tasks.LostReplies == 0 || out.Tasks.Resyncs < int64(len(targets)) {
		t.Fatalf("tasks %+v for %d tasks on %d nodes", out.Tasks, len(tasks), len(targets))
	}
	// After lost cursors and resyncs, every agent's cursor is control's record again.
	cursors := map[string]string{}
	for _, row := range strings.Split(e.sql(t, "SELECT id||'='||task_acknowledged_sequence FROM managed_nodes WHERE id IN ("+fleet.nodeList()+")"), "\n") {
		id, sequence, _ := strings.Cut(row, "=")
		cursors[id] = sequence
	}
	for _, identity := range fleet.identities(t) {
		if cursors[identity.NodeID] != strconv.FormatUint(identity.AcknowledgedSequence, 10) {
			t.Fatalf("node %s: agent cursor %d, control %q", identity.NodeID, identity.AcknowledgedSequence, cursors[identity.NodeID])
		}
	}
}

// mintTokens mints n read-only join tokens through the administrator API.
func (e environment) mintTokens(t *testing.T, admin *browser, csrf string, n int) []string {
	t.Helper()
	var workspaces struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	status, raw := admin.request(t, http.MethodGet, "/api/v1/admin/workspaces", nil, "")
	decode(t, status, http.StatusOK, raw, &workspaces)
	if len(workspaces.Items) == 0 {
		t.Fatal("no workspace")
	}
	tokens := make([]string, 0, n)
	for i := range n {
		var token struct {
			Token string `json:"token"`
		}
		status, raw = admin.request(t, http.MethodPost, "/api/v1/sites/default/cells/default/admin/node-join-tokens", map[string]string{"workspace_id": workspaces.Items[0].ID, "node_name": fmt.Sprintf("sim-%06d", i), "execution_profile": "read_only"}, csrf)
		decode(t, status, http.StatusCreated, raw, &token)
		tokens = append(tokens, token.Token)
	}
	return tokens
}
