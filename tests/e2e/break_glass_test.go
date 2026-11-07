package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The proof of ARC-016 (docs/research/operator-break-glass.md).
const (
	breakGlassSeconds = 1500
	// breakGlassSeats is the default pool less the installation lease and the operator's reserve.
	breakGlassSeats = 6
	// breakGlassAnswer is how long a sign-in or a console read may take with every seat busy.
	breakGlassAnswer = 3 * time.Second
	// breakGlassHold is how long agents' calls are kept waiting on a lock.
	breakGlassHold = 40 * time.Second
	// breakGlassSpare is how far the connection limit sits above the fleet.
	breakGlassSpare = 64
	// breakGlassReturn is the time to get a fleet back, threshold 4 of the load test.
	breakGlassReturn = 300 * time.Second
	// breakGlassOffline is the online window of the default policy, with room for a flush.
	breakGlassOffline = 45 * time.Second
	shedPath          = "/api/v1/sites/default/cells/default/admin/agent-shed"
	// breakGlassFullFor is how long the table stays full: less than control's read-header timeout.
	breakGlassFullFor = 4 * time.Second
)

// agents runs a subcommand of `ownsolid-install agents` against this install.
func (e environment) agents(t *testing.T, config string, args ...string) string {
	t.Helper()
	output, err := run(t, e.installer, append([]string{"agents", args[0], "--config", config}, args[1:]...)...)
	if err != nil {
		t.Fatalf("agents %s: %v\n%s", args[0], err, output)
	}
	return output
}

// waitFor polls until ready says so, and returns how long that took.
func waitFor(t *testing.T, name string, within time.Duration, ready func() bool) time.Duration {
	t.Helper()
	started := time.Now()
	for !ready() {
		if time.Since(started) > within {
			t.Fatalf("%s: not within %s", name, within)
		}
		time.Sleep(time.Second)
	}
	return time.Since(started)
}

// setShed changes the level the way the console does and returns it.
func setShed(t *testing.T, admin *browser, level, reason string) {
	t.Helper()
	var current struct {
		Version int64 `json:"version"`
	}
	status, raw := admin.request(t, http.MethodGet, shedPath, nil, "")
	decode(t, status, http.StatusOK, raw, &current)
	status, raw = admin.request(t, http.MethodPut, shedPath, map[string]any{"level": level, "version": current.Version, "reason": reason}, admin.csrfToken(t))
	if status != http.StatusOK {
		t.Fatalf("set level %s: %d %s", level, status, raw)
	}
}

// heardSince counts the fleet's nodes that reached the database after a moment.
func (e environment) heardSince(t *testing.T, f *simulatedRun, moment time.Time) int {
	t.Helper()
	count := e.sql(t, "SELECT count(*) FROM managed_nodes WHERE id IN ("+f.nodeList()+") AND last_heartbeat_at > '"+moment.UTC().Format(time.RFC3339Nano)+"'")
	n, err := strconv.Atoi(count)
	if err != nil {
		t.Fatal(count, err)
	}
	return n
}

// fleetReturns waits for every agent to be heard after the level was lifted.
func (e environment) fleetReturns(t *testing.T, f *simulatedRun, name string) {
	t.Helper()
	lifted := time.Now()
	took := waitFor(t, name, breakGlassReturn, func() bool { return e.heardSince(t, f, lifted) == len(f.nodes) })
	t.Logf("%s: every agent heard from %s after the level was lifted", name, took.Round(100*time.Millisecond))
}

// connectionTableFull reports that a new connection gets no answer.
func (e environment) connectionTableFull(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, adminOrigin+"/health/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := e.newBrowser(t, adminOrigin).client.Do(request)
	if err != nil {
		return true
	}
	response.Body.Close()
	return false
}

// controlStarted is when the running control container started.
func controlStarted(t *testing.T) string {
	t.Helper()
	output, err := run(t, "docker", "inspect", "--format", "{{.State.StartedAt}} {{.State.Running}}", "ownsolid-control-1")
	if err != nil {
		t.Fatalf("inspect control: %v %s", err, output)
	}
	return strings.TrimSpace(output)
}

func TestBreakGlass(t *testing.T) {
	if os.Getenv("OWNSOLID_E2E_LOAD") == "" {
		t.Skip("needs OWNSOLID_E2E_LOAD, and OWNSOLID_E2E_RUN=TestBreakGlass in run-single-node.sh")
	}
	e := loadEnvironment(t)
	agents := loadAgents
	if value := os.Getenv("OWNSOLID_E2E_SIMULATED_AGENTS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 100 {
			t.Fatalf("OWNSOLID_E2E_SIMULATED_AGENTS=%q", value)
		}
		agents = n
	}
	directory := t.TempDir()
	limits := strings.Replace(fleetLimits, "max_connections: 4096", fmt.Sprintf("max_connections: %d", agents+breakGlassSpare), 1)
	config := e.configurationWith(t, directory, "single-node", e.controlImage, limits)
	e.apply(t, config)
	admin, csrf := e.adminSession(t, e.password(t))
	e.installLicense(t, admin, csrf)
	e.setPacing(t, admin, csrf, loadHeartbeat, 10, 1, 60, "Break-glass drill at twice the planned rate")
	fleet := e.enrollFleet(t, admin, csrf, agents, map[string]any{"duration_seconds": breakGlassSeconds, "window_seconds": 60})
	fleet.start(t, 90*time.Second)

	t.Run("a sign-in and a console read work with every seat busy", func(t *testing.T) {
		started := controlStarted(t)
		holding := make(chan error, 1)
		go func() {
			query := fmt.Sprintf("BEGIN; LOCK TABLE managed_nodes, node_tasks IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(%d); COMMIT", int(breakGlassHold.Seconds()))
			output, err := e.compose(t, "exec", "--no-TTY", "postgres", "psql", "-X", "-tA", "-v", "ON_ERROR_STOP=1", "-U", "ownsolid_migrator", "-d", "ownsolid", "-c", query)
			if err != nil {
				err = fmt.Errorf("%w: %s", err, output)
			}
			holding <- err
		}()
		blocked := 0
		waitFor(t, "agents to fill every seat", breakGlassHold/2, func() bool {
			blocked, _ = strconv.Atoi(e.sql(t, "SELECT count(*) FROM pg_stat_activity WHERE usename='ownsolid_runtime' AND wait_event_type='Lock'"))
			return blocked >= breakGlassSeats
		})
		t.Logf("%d runtime connections wait on the lock; control's connections by state and statement:\n%s", blocked,
			e.sql(t, "SELECT count(*)||' '||state||' '||COALESCE(wait_event_type,'-')||' '||regexp_replace(left(query,70),'\\s+',' ','g') FROM pg_stat_activity WHERE usename='ownsolid_runtime' GROUP BY state,wait_event_type,left(query,70) ORDER BY count(*) DESC"))
		began := time.Now()
		visitor := e.newBrowser(t, adminOrigin)
		if status := visitor.login(t, e.password(t)); status != http.StatusOK {
			t.Fatalf("sign-in: %d", status)
		}
		signIn := time.Since(began)
		began = time.Now()
		status, raw := visitor.request(t, http.MethodGet, "/api/v1/admin/license", nil, "")
		if status != http.StatusOK {
			t.Fatalf("console read: %d %s", status, raw)
		}
		read := time.Since(began)
		t.Logf("with %d connections blocked: sign-in %s, console read %s", blocked, signIn.Round(time.Millisecond), read.Round(time.Millisecond))
		if signIn > breakGlassAnswer || read > breakGlassAnswer {
			t.Errorf("slower than %s", breakGlassAnswer)
		}
		if err := <-holding; err != nil {
			t.Fatal(err)
		}
		// A lock is not a different schema: control must still be the process that started.
		time.Sleep(10 * time.Second)
		if now := controlStarted(t); now != started {
			t.Fatalf("control restarted during the hold: started %s, now %s", started, now)
		}
	})

	t.Run("the console refuses agents, audits it and lifts it", func(t *testing.T) {
		setShed(t, admin, "heartbeats", "Break-glass drill from the console")
		took := waitFor(t, "every node to turn offline", breakGlassOffline+30*time.Second, func() bool { return e.online(t, fleet) == "0" })
		t.Logf("every node offline %s after the level was set", took.Round(time.Second))
		if audited := e.sql(t, "SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND reason='Break-glass drill from the console' AND actor_user_id IS NOT NULL"); audited != "1" {
			t.Fatalf("%s audit records", audited)
		}
		logs, err := e.compose(t, "logs", "--no-color", "control")
		if err != nil || !strings.Contains(logs, "agent_call_refused priority=heartbeat reason=shed code=AGENT_OVERLOADED") {
			t.Fatalf("no refusal for shedding in control's log: %v", err)
		}
		setShed(t, admin, "none", "Break-glass drill over")
		e.fleetReturns(t, fleet, "after the console lifted it")
	})

	t.Run("the host sets a level with the connection limit full", func(t *testing.T) {
		// Connections that never finish a TLS handshake fill the table until control's read-header
		// timeout closes them, so everything below happens well inside it.
		filled := time.Now()
		var held []net.Conn
		for len(held) < 4*breakGlassSpare {
			connection, err := net.DialTimeout("tcp", "127.0.0.1:8443", time.Second)
			if err != nil {
				break
			}
			held = append(held, connection)
		}
		defer func() {
			for _, connection := range held {
				connection.Close()
			}
		}()
		if !e.connectionTableFull(t) {
			t.Fatal("the connection limit was not full, so this would prove nothing")
		}
		output := e.agents(t, config, "shed", "--level", "bulk", "--reason", "Connection table full")
		if took := time.Since(filled); took > breakGlassFullFor {
			t.Fatalf("the shed took until %s after the table filled, past the read-header timeout", took)
		}
		t.Logf("with the connection limit full: %s", strings.TrimSpace(output))
		if !strings.Contains(output, "AGENT_SHED_LEVEL bulk") {
			t.Fatal(output)
		}
		if audited := e.sql(t, "SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND audience='operator' AND reason='Connection table full'"); audited != "1" {
			t.Fatalf("%s audit records", audited)
		}
		for _, connection := range held {
			connection.Close()
		}
		e.agents(t, config, "shed", "--level", "none", "--reason", "Connection table free")
	})

	var stopped time.Time
	t.Run("a level set while the database is stopped holds the fleet back when control returns", func(t *testing.T) {
		if output, err := e.compose(t, "stop", "postgres"); err != nil {
			t.Fatalf("stop postgres: %v %s", err, output)
		}
		stopped = time.Now()
		output := e.agents(t, config, "shed", "--level", "heartbeats", "--reason", "Database stopped")
		t.Logf("with the database stopped: %s", strings.TrimSpace(output))
		if !strings.Contains(output, "AGENT_SHED_NOT_STORED") && !strings.Contains(output, "AGENT_SHED_HELD heartbeats") {
			t.Fatal(output)
		}
		held := filepath.Join(e.root, "socket", "agent-shed.json")
		if _, err := os.Stat(held); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "control to stop without its database", time.Minute, func() bool {
			_, err := run(t, e.installer, "agents", "status", "--config", config)
			status, _ := e.compose(t, "ps", "--status", "running", "--services")
			return err == nil && !strings.Contains(status, "control")
		})
		if output, err := e.compose(t, "start", "postgres"); err != nil {
			t.Fatalf("start postgres: %v %s", err, output)
		}
		waitFor(t, "control to store and audit the held level", 3*time.Minute, func() bool {
			return e.sql(t, "SELECT count(*) FROM audit_events WHERE action='node.agents_shed' AND audience='operator' AND reason='Database stopped'") == "1"
		})
		if level := e.sql(t, "SELECT shed_level FROM agent_pacing"); level != "heartbeats" {
			t.Fatal(level)
		}
		if _, err := os.Stat(held); !os.IsNotExist(err) {
			t.Fatalf("a stored level stayed on the host: %v", err)
		}
		time.Sleep(3 * loadHeartbeat * time.Second)
		if heard := e.heardSince(t, fleet, stopped); heard != 0 {
			t.Fatalf("%d agents were let in by the restart", heard)
		}
	})

	t.Run("a stored level survives a restart and the host lifts it", func(t *testing.T) {
		if output, err := e.compose(t, "restart", "control"); err != nil {
			t.Fatalf("restart control: %v %s", err, output)
		}
		waitFor(t, "the socket to return", time.Minute, func() bool {
			output, err := run(t, e.installer, "agents", "status", "--config", config)
			return err == nil && strings.Contains(output, "AGENT_SHED_LEVEL heartbeats")
		})
		time.Sleep(3 * loadHeartbeat * time.Second)
		if heard := e.heardSince(t, fleet, stopped); heard != 0 {
			t.Fatalf("%d agents were let in by the restart", heard)
		}
		e.agents(t, config, "shed", "--level", "none", "--reason", "Break-glass drill over on the host")
		e.fleetReturns(t, fleet, "after the host lifted it")
	})
}
