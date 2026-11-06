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
const defaultSimulatedAgents = 1000

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
	Faults []struct {
		Kind         string   `json:"kind"`
		Agents       int      `json:"agents"`
		Measured     bool     `json:"measured"`
		Recovered    int      `json:"recovered_agents"`
		FullRecovery *float64 `json:"full_recovery_seconds"`
		P99Recovery  *float64 `json:"p99_recovery_seconds"`
	} `json:"faults"`
}

// simulatedFleet enrolls a simulated fleet with real join tokens, runs it through the
// scripted faults while an administrator runs tasks on part of it, and checks the
// simulator's report against control's own record.
func (e environment) simulatedFleet(t *testing.T, password string) {
	t.Helper()
	agents := defaultSimulatedAgents
	if value := os.Getenv("OWNSOLID_E2E_SIMULATED_AGENTS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 {
			t.Fatalf("OWNSOLID_E2E_SIMULATED_AGENTS=%q", value)
		}
		agents = n
	}
	admin, csrf := e.adminSession(t, password)
	e.installLicense(t, admin, csrf)
	directory := t.TempDir()
	tokens := e.mintTokens(t, admin, csrf, agents)
	writePrivate(t, filepath.Join(directory, "tokens"), []byte(strings.Join(tokens, "\n")+"\n"))
	authority, err := os.ReadFile(filepath.Join(e.root, "config", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	writePrivate(t, filepath.Join(directory, "ca.pem"), authority)
	configuration, _ := json.Marshal(map[string]any{
		"control_origin": adminOrigin, "ca_file": filepath.Join(directory, "ca.pem"), "dial_address": "127.0.0.1:8443",
		"identities_file": filepath.Join(directory, "identities.json"), "join_tokens_file": filepath.Join(directory, "tokens"),
		"agents": agents, "enroll_concurrency": 16, "duration_seconds": simulatedSeconds, "start_seconds": 10,
		"report_file": filepath.Join(directory, "report.json"), "faults": fleetFaults,
	})
	configFile := filepath.Join(directory, "simulator.json")
	writePrivate(t, configFile, configuration)

	started := time.Now()
	if output, err := run(t, e.simulator, "enroll", "--config", configFile); err != nil {
		t.Fatalf("enroll: %v\n%s", err, output)
	}
	t.Logf("%d simulated agents enrolled in %s", agents, time.Since(started).Round(time.Second))
	var identities struct {
		Agents []simulatedIdentity `json:"agents"`
	}
	raw, err := os.ReadFile(filepath.Join(directory, "identities.json"))
	if err != nil || json.Unmarshal(raw, &identities) != nil || len(identities.Agents) != agents {
		t.Fatalf("identities: %v", err)
	}
	// Tasks go to every twentieth node, a few dozen at a time.
	var targets []string
	for i := 0; i < agents; i += 20 {
		targets = append(targets, identities.Agents[i].NodeID)
	}

	log := &lockedBuffer{}
	command := exec.Command(e.simulator, "run", "--config", configFile)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	var exit error
	go func() {
		exit = command.Wait()
		close(finished)
	}()
	running := time.Now()
	defer func() {
		select {
		case <-finished:
		default:
			command.Process.Signal(syscall.SIGTERM)
			<-finished
		}
	}()
	at := func(seconds int) { time.Sleep(time.Until(running.Add(time.Duration(seconds) * time.Second))) }
	connected := fmt.Sprintf("agents=%d connected=%d ", agents, agents)
	for !strings.Contains(log.String(), connected) {
		if time.Since(running) > 50*time.Second {
			t.Fatalf("the fleet did not connect:\n%s", log.String())
		}
		time.Sleep(time.Second)
	}
	t.Logf("every simulated agent connected %s after the start", time.Since(running).Round(time.Second))

	var tasks []string
	batch := func(name string, within time.Duration) {
		t.Helper()
		created := time.Now()
		var ids []string
		for _, node := range targets {
			var task struct {
				ID string `json:"id"`
			}
			status, raw := admin.requestWith(t, http.MethodPost, nodesPath+"/"+node+"/tasks", map[string]string{"operation": "collect_facts.v1"}, csrf, map[string]string{"Idempotency-Key": newUUID(t)})
			decode(t, status, http.StatusAccepted, raw, &task)
			ids = append(ids, task.ID)
		}
		list := "'" + strings.Join(ids, "','") + "'"
		for {
			done := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+list+") AND state='succeeded' AND attempt>0 AND acknowledged_attempt=attempt")
			if done == strconv.Itoa(len(ids)) {
				break
			}
			if time.Since(created) > within {
				t.Fatalf("%s: %s of %d tasks succeeded within %s\n%s", name, done, len(ids), within,
					e.sql(t, "SELECT string_agg(state||':'||count, ', ') FROM (SELECT state, count(*) FROM node_tasks WHERE id IN ("+list+") GROUP BY state) s"))
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Logf("%s: %d tasks succeeded %s after they were created", name, len(ids), time.Since(created).Round(100*time.Millisecond))
		tasks = append(tasks, ids...)
	}
	batch("steady fleet", 30*time.Second)
	at(140)
	// Agents healed from the partition may still be in backoff, for up to the reconnect
	// maximum of the default pacing.
	batch("replies lost", 90*time.Second)
	at(210)
	batch("after a fleet restart", 30*time.Second)

	select {
	case <-finished:
		if exit != nil {
			t.Fatalf("simulator: %v\n%s", exit, log.String())
		}
	case <-time.After(time.Until(running.Add((simulatedSeconds + 60) * time.Second))):
		t.Fatalf("the simulator did not stop:\n%s", log.String())
	}
	// Each node's last heartbeat reached the database within the online window.
	online := e.sql(t, "SELECT count(*) FROM managed_nodes WHERE id IN ('"+strings.Join(nodeIDs(identities.Agents), "','")+"') AND last_heartbeat_at > clock_timestamp() - interval '30 seconds'")
	if online != strconv.Itoa(agents) {
		t.Fatalf("%s of %d simulated nodes are online at the end", online, agents)
	}

	var out fleetReport
	raw, err = os.ReadFile(filepath.Join(directory, "report.json"))
	if err != nil || json.Unmarshal(raw, &out) != nil {
		t.Fatalf("report: %v", err)
	}
	t.Logf("report:\n%s", raw)
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
	for _, row := range strings.Split(e.sql(t, "SELECT id||'='||task_acknowledged_sequence FROM managed_nodes WHERE id IN ('"+strings.Join(nodeIDs(identities.Agents), "','")+"')"), "\n") {
		id, sequence, _ := strings.Cut(row, "=")
		cursors[id] = sequence
	}
	raw, err = os.ReadFile(filepath.Join(directory, "identities.json"))
	if err != nil || json.Unmarshal(raw, &identities) != nil {
		t.Fatalf("identities after the run: %v", err)
	}
	for _, identity := range identities.Agents {
		if cursors[identity.NodeID] != strconv.FormatUint(identity.AcknowledgedSequence, 10) {
			t.Fatalf("node %s: agent cursor %d, control %q", identity.NodeID, identity.AcknowledgedSequence, cursors[identity.NodeID])
		}
	}
	if !strings.Contains(log.String(), "simulator_finished") {
		t.Fatalf("simulator log:\n%s", log.String())
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

func nodeIDs(identities []simulatedIdentity) []string {
	ids := make([]string, len(identities))
	for i, identity := range identities {
		ids[i] = identity.NodeID
	}
	return ids
}
