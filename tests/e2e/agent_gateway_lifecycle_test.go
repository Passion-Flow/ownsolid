package e2e

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gatewayRetirement struct {
	reason              string
	ageSeconds          float64
	scheduledAgeSeconds float64
	startedAgeSeconds   float64
	observed            bool
	forced              bool
}

type gatewayConnectionState struct {
	active  map[string]string
	retired map[string]gatewayRetirement
}

// gatewayConnections keeps a new session when an older close log arrives later.
// Empty connection IDs retain compatibility with the earlier gateway logs.
func gatewayConnections(t *testing.T, name string) gatewayConnectionState {
	t.Helper()
	output, err := run(t, "docker", "logs", name)
	if err != nil {
		t.Fatalf("logs %s: %v %s", name, err, output)
	}
	state, err := parseGatewayConnections(output)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func parseGatewayConnections(output string) (gatewayConnectionState, error) {
	state := gatewayConnectionState{active: map[string]string{}, retired: map[string]gatewayRetirement{}}
	for _, line := range strings.Split(output, "\n") {
		var entry struct {
			Message             string   `json:"msg"`
			Node                string   `json:"node_id"`
			ConnectionID        string   `json:"connection_id"`
			Reason              string   `json:"reason"`
			AgeSeconds          *float64 `json:"age_seconds"`
			ScheduledAgeSeconds *float64 `json:"scheduled_age_seconds"`
			StartedAgeSeconds   *float64 `json:"drain_started_age_seconds"`
			Forced              *bool    `json:"forced"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			for _, field := range strings.Fields(line) {
				key, value, _ := strings.Cut(field, "=")
				value = strings.Trim(value, `"`)
				switch key {
				case "msg":
					entry.Message = value
				case "node_id":
					entry.Node = value
				case "connection_id":
					entry.ConnectionID = value
				case "reason":
					entry.Reason = value
				case "age_seconds", "scheduled_age_seconds", "drain_started_age_seconds":
					number, err := strconv.ParseFloat(value, 64)
					if err != nil {
						return state, fmt.Errorf("invalid gateway age observation: %w", err)
					}
					switch key {
					case "age_seconds":
						entry.AgeSeconds = &number
					case "scheduled_age_seconds":
						entry.ScheduledAgeSeconds = &number
					case "drain_started_age_seconds":
						entry.StartedAgeSeconds = &number
					}
				case "forced":
					forced, err := strconv.ParseBool(value)
					if err != nil {
						return state, fmt.Errorf("invalid gateway closure observation: %w", err)
					}
					entry.Forced = &forced
				}
			}
		}
		switch entry.Message {
		case "agent_gateway_started":
			state = gatewayConnectionState{active: map[string]string{}, retired: map[string]gatewayRetirement{}}
		case "agent_session_opened":
			if entry.Node != "" {
				state.active[entry.Node] = entry.ConnectionID
			}
		case "agent_session_closed":
			if entry.ConnectionID == "" || state.active[entry.Node] == entry.ConnectionID {
				delete(state.active, entry.Node)
			}
		case "http_connection_retired":
			if entry.ConnectionID == "" || entry.AgeSeconds == nil || entry.Forced == nil ||
				math.IsNaN(*entry.AgeSeconds) || math.IsInf(*entry.AgeSeconds, 0) || *entry.AgeSeconds < 0 {
				return state, fmt.Errorf("gateway retirement has an invalid transport identity or outcome")
			}
			retirement := gatewayRetirement{reason: entry.Reason, ageSeconds: *entry.AgeSeconds, forced: *entry.Forced}
			if entry.ScheduledAgeSeconds != nil || entry.StartedAgeSeconds != nil {
				if entry.ScheduledAgeSeconds == nil || entry.StartedAgeSeconds == nil {
					return state, fmt.Errorf("gateway retirement has incomplete observed ages")
				}
				for _, number := range []float64{*entry.ScheduledAgeSeconds, *entry.StartedAgeSeconds} {
					if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
						return state, fmt.Errorf("gateway retirement has an invalid observed age")
					}
				}
				if *entry.AgeSeconds < *entry.StartedAgeSeconds || entry.Reason == "connection_age" && *entry.StartedAgeSeconds < *entry.ScheduledAgeSeconds {
					return state, fmt.Errorf("gateway retirement has inconsistent observed timing")
				}
				retirement.scheduledAgeSeconds = *entry.ScheduledAgeSeconds
				retirement.startedAgeSeconds = *entry.StartedAgeSeconds
				retirement.observed = true
			}
			state.retired[entry.ConnectionID] = retirement
		}
	}
	return state, nil
}

func TestGatewayConnectionLogOrdering(t *testing.T) {
	const opened = `{"msg":"agent_session_opened","node_id":"node-a","connection_id":"old"}`
	const retired = `{"msg":"http_connection_retired","connection_id":"old","reason":"connection_age","age_seconds":58.5,"forced":false}`
	for _, tc := range []struct {
		name, logs, activeID string
		active, retired      bool
	}{
		{"delayed_old_close", opened + "\n" + `{"msg":"agent_session_opened","node_id":"node-a","connection_id":"new"}` + "\n" + `{"msg":"agent_session_closed","node_id":"node-a","connection_id":"old"}` + "\n" + retired, "new", true, true},
		{"current_close", opened + "\n" + `{"msg":"agent_session_closed","node_id":"node-a","connection_id":"old"}`, "", false, false},
		{"restart", opened + "\n" + retired + "\ntime=fixture msg=agent_gateway_started\n" + `time=fixture msg=agent_session_opened node_id=node-a connection_id=new`, "new", true, false},
		{"legacy", "msg=agent_session_opened node_id=node-a\nmsg=agent_session_closed node_id=node-a", "", false, false},
		{"text_retirement", "msg=agent_session_opened node_id=node-a connection_id=old\nmsg=http_connection_retired connection_id=old reason=connection_age age_seconds=58.5 forced=false", "old", true, true},
		{"unrelated_message", opened + "\n" + `{"msg":"fixture_diagnostic","detail":"agent_gateway_started"}` + "\n" + retired, "old", true, true},
		{"unrelated_text", opened + "\nmsg=fixture_diagnostic detail=agent_gateway_started\n" + retired, "old", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := parseGatewayConnections(tc.logs)
			if err != nil {
				t.Fatal(err)
			}
			id, active := state.active["node-a"]
			retirement, retired := state.retired["old"]
			if active != tc.active || id != tc.activeID || retired != tc.retired {
				t.Fatalf("unexpected connection lifecycle: %+v", state)
			}
			if retired && (retirement.reason != "connection_age" || retirement.forced || retirement.ageSeconds != 58.5) {
				t.Fatalf("lost retirement outcome: %+v", retirement)
			}
		})
	}
	for _, logs := range []string{
		`{"msg":"http_connection_retired","connection_id":"old","age_seconds":58.5}`,
		`msg=http_connection_retired connection_id=old age_seconds=invalid forced=false`,
		`msg=http_connection_retired connection_id=old age_seconds=58.5 forced=invalid`,
		`msg=http_connection_retired connection_id=old age_seconds=NaN forced=false`,
		`msg=http_connection_retired connection_id=old age_seconds=+Inf forced=false`,
		`{"msg":"http_connection_retired","connection_id":"old","age_seconds":-1,"forced":false}`,
	} {
		if _, err := parseGatewayConnections(logs); err == nil {
			t.Fatal("incomplete retirement observation accepted", logs)
		}
	}
}

func TestAgentGatewayConnectionLifecycle(t *testing.T) {
	if os.Getenv("OWNSOLID_E2E_LOAD") == "" {
		t.Skip("needs the isolated installed gateway load profile")
	}
	const maximumAgeSeconds = 60
	e := loadEnvironment(t)
	configuration := e.configurationWith(t, t.TempDir(), "single-node", e.controlImage,
		fleetLimits+"resources: {database_role_connections: 30}\n")
	e.apply(t, configuration)
	admin, csrf := e.adminSession(t, e.password(t))
	e.installLicense(t, admin, csrf)
	e.setPacing(t, admin, csrf, gatewayInterval, 10, 1, 30, "Gateway lifecycle drill")
	first := e.startGatewayWithAge(t, "ownsolid-age-gateway-a", 19443, maximumAgeSeconds)
	second := e.startGatewayWithAge(t, "ownsolid-age-gateway-b", 19444, maximumAgeSeconds)
	observer := startGatewayObserver(t, first, second)
	fleet := e.enrollFleet(t, admin, csrf, gatewayAgents, map[string]any{
		"duration_seconds": gatewaySeconds, "window_seconds": 5,
		"session_addresses": []string{first.address, second.address}, "gateway_rebalance_seconds": 5,
		"long_task_seconds": 100,
		"faults":            []map[string]any{{"kind": "long_tasks", "at_seconds": 0, "duration_seconds": 300, "percent": 100}},
	})
	fleet.start(t, 2*time.Minute)
	e.fleetOnline(t, fleet, "initial gateway lifecycle fleet", fleet.started, gatewayMove)
	tasks := createTasks(t, admin, csrf, fleet.targets())
	taskList := "'" + strings.Join(tasks, "','") + "'"
	waitFor(t, "acknowledged work before gateway age", 15*time.Second, func() bool {
		return e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+taskList+") AND state='running' AND attempt=1 AND acknowledged_attempt=attempt") == strconv.Itoa(len(tasks))
	})
	var originalFirst, originalSecond gatewayConnectionState
	waitFor(t, "complete original gateway assignment", gatewayMove, func() bool {
		originalFirst, originalSecond = gatewayConnections(t, first.name), gatewayConnections(t, second.name)
		return len(originalFirst.active)+len(originalSecond.active) == gatewayAgents &&
			len(originalFirst.active) >= gatewayAgents/3 && len(originalSecond.active) >= gatewayAgents/3
	})
	identities := map[string]bool{}
	for _, original := range []gatewayConnectionState{originalFirst, originalSecond} {
		for _, connectionID := range original.active {
			if connectionID == "" || identities[connectionID] {
				t.Fatal("the installed fleet did not retain one distinct transport per agent")
			}
			identities[connectionID] = true
		}
	}
	t.Logf("initial sessions: %d/%d; acknowledged tasks: %d", len(originalFirst.active), len(originalSecond.active), len(tasks))
	pairs := []struct {
		gateway agentGateway
		before  gatewayConnectionState
	}{{first, originalFirst}, {second, originalSecond}}
	aged := time.Now()
	observer.checkpoint(t, "automatic_age")
	waitFor(t, "every original transport retires at age and reconnects", gatewayMove, func() bool {
		for _, pair := range pairs {
			current := gatewayConnections(t, pair.gateway.name)
			for node, previousID := range pair.before.active {
				retirement, ok := current.retired[previousID]
				if !ok || current.active[node] == "" || current.active[node] == previousID {
					return false
				}
				if retirement.reason != "connection_age" || retirement.forced || !retirement.observed ||
					retirement.scheduledAgeSeconds < 0.9*maximumAgeSeconds || retirement.scheduledAgeSeconds > maximumAgeSeconds {
					t.Fatalf("invalid automatic retirement: %+v", retirement)
				}
			}
		}
		return true
	})
	for _, pair := range pairs {
		current := gatewayConnections(t, pair.gateway.name)
		minimumStart, minimumRetirement := math.Inf(1), math.Inf(1)
		maximumStart, maximumRetirement, maximumDelay := 0.0, 0.0, 0.0
		for _, id := range pair.before.active {
			retirement, ok := current.retired[id]
			if !ok || !retirement.observed {
				t.Fatal("original transport lost its elapsed timing observation")
			}
			minimumStart = min(minimumStart, retirement.startedAgeSeconds)
			maximumStart = max(maximumStart, retirement.startedAgeSeconds)
			minimumRetirement = min(minimumRetirement, retirement.ageSeconds)
			maximumRetirement = max(maximumRetirement, retirement.ageSeconds)
			maximumDelay = max(maximumDelay, retirement.startedAgeSeconds-retirement.scheduledAgeSeconds)
		}
		t.Logf("gateway=%s original_transports=%d observed_drain_age_seconds_min=%.6f max=%.6f observed_retirement_age_seconds_min=%.6f max=%.6f drain_delay_seconds_max=%.6f", pair.gateway.name, len(pair.before.active), minimumStart, maximumStart, minimumRetirement, maximumRetirement, maximumDelay)
	}
	t.Logf("all %d original connections replaced after %s", gatewayAgents, time.Since(aged).Round(100*time.Millisecond))
	observer.checkpoint(t, "after_automatic_age")
	if running := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+taskList+") AND state='running' AND attempt=1 AND acknowledged_attempt=1"); running != strconv.Itoa(len(tasks)) {
		t.Fatal("the age drill did not retain every acknowledged task in flight")
	}
	e.fleetOnline(t, fleet, "after automatic age", aged, gatewayMove)
	e.waitForTasks(t, "acknowledged tasks retained through age", tasks, gatewayDispatch)
	if attempts := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+taskList+") AND attempt=1 AND acknowledged_attempt=1"); attempts != strconv.Itoa(len(tasks)) {
		t.Fatal("connection age replaced an acknowledged task attempt")
	}
	rolledTasks := createTasks(t, admin, csrf, fleet.targets())
	rolledList := "'" + strings.Join(rolledTasks, "','") + "'"
	waitFor(t, "acknowledged work before gateway roll", 15*time.Second, func() bool {
		return e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+rolledList+") AND state='running' AND attempt=1 AND acknowledged_attempt=attempt") == strconv.Itoa(len(rolledTasks))
	})
	observer.checkpoint(t, "gateway_a_stopped")
	if output, err := run(t, "docker", "kill", first.name); err != nil {
		t.Fatalf("kill gateway: %v %s", err, output)
	}
	waitFor(t, "all agents on surviving gateway", gatewayMove, func() bool {
		return len(gatewayConnections(t, second.name).active) == gatewayAgents
	})
	observer.checkpoint(t, "gateway_a_stopped")
	if output, err := run(t, "docker", "start", first.name); err != nil {
		t.Fatalf("return gateway: %v %s", err, output)
	}
	first.waitReady(t, e, time.Minute)
	returned := time.Now()
	observer.checkpoint(t, "gateway_returned")
	waitFor(t, "every agent returns to its original preferred gateway", gatewayMove, func() bool {
		for _, pair := range pairs {
			current := gatewayConnections(t, pair.gateway.name)
			if len(current.active) != len(pair.before.active) {
				return false
			}
			for node := range pair.before.active {
				if current.active[node] == "" {
					return false
				}
			}
		}
		return true
	})
	t.Logf("original %d/%d distribution restored after %s", len(originalFirst.active), len(originalSecond.active), time.Since(returned).Round(100*time.Millisecond))
	observer.checkpoint(t, "original_assignments_restored")
	if running := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+rolledList+") AND state='running' AND attempt=1 AND acknowledged_attempt=1"); running != strconv.Itoa(len(rolledTasks)) {
		t.Fatal("the gateway roll did not retain every acknowledged task in flight")
	}
	e.fleetOnline(t, fleet, "after preferred gateways return", returned, gatewayMove)
	e.waitForTasks(t, "acknowledged tasks retained through gateway roll", rolledTasks, gatewayDispatch)
	if attempts := e.sql(t, "SELECT count(*) FROM node_tasks WHERE id IN ("+rolledList+") AND attempt=1 AND acknowledged_attempt=1"); attempts != strconv.Itoa(len(rolledTasks)) {
		t.Fatal("gateway roll replaced an acknowledged task attempt")
	}
	fleet.stop(t, time.Minute)
	report := fleet.report(t)
	if report.Agents != gatewayAgents || report.Connected != gatewayAgents || report.OnStream != gatewayAgents ||
		report.Revoked != 0 || report.Calls["gateway_rebalance"].Succeeded == 0 || report.Calls["renew"].Succeeded == 0 {
		t.Fatalf("lifecycle fleet did not retain streams, renewals and preference checks: %+v", report)
	}
	e.requireFleetCursors(t, fleet)
}

func TestGatewayObservedRetirementTiming(t *testing.T) {
	for _, line := range []string{
		`{"msg":"http_connection_retired","connection_id":"observed","reason":"connection_age","scheduled_age_seconds":58.5,"drain_started_age_seconds":58.6,"age_seconds":60.5,"forced":false}`,
		`msg=http_connection_retired connection_id=observed reason=connection_age scheduled_age_seconds=58.5 drain_started_age_seconds=58.6 age_seconds=60.5 forced=false`,
	} {
		state, err := parseGatewayConnections(line)
		if err != nil {
			t.Fatal(err)
		}
		got := state.retired["observed"]
		if !got.observed || got.scheduledAgeSeconds != 58.5 || got.startedAgeSeconds != 58.6 || got.ageSeconds != 60.5 || got.forced {
			t.Fatalf("lost planned or measured timing: %+v", got)
		}
	}
	early := `{"msg":"http_connection_retired","connection_id":"early","reason":"process_shutdown","scheduled_age_seconds":58.5,"drain_started_age_seconds":0.1,"age_seconds":0.2,"forced":false}`
	if state, err := parseGatewayConnections(early); err != nil || !state.retired["early"].observed || state.retired["early"].ageSeconds != 0.2 {
		t.Fatal("early process stop lost its actual age", err)
	}
	legacy := `{"msg":"http_connection_retired","connection_id":"legacy","reason":"connection_age","age_seconds":58.5,"forced":false}`
	if state, err := parseGatewayConnections(legacy); err != nil || state.retired["legacy"].observed {
		t.Fatal("legacy planning log established an observed age", err)
	}
	for _, line := range []string{
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=58.5 age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age drain_started_age_seconds=58.6 age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=-1 drain_started_age_seconds=58.6 age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=58.5 drain_started_age_seconds=NaN age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=+Inf drain_started_age_seconds=58.6 age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=58.5 drain_started_age_seconds=58.4 age_seconds=60.5 forced=false`,
		`msg=http_connection_retired connection_id=bad reason=connection_age scheduled_age_seconds=58.5 drain_started_age_seconds=58.6 age_seconds=58.5 forced=false`,
	} {
		if _, err := parseGatewayConnections(line); err == nil {
			t.Fatal("incomplete or inconsistent elapsed timing accepted", line)
		}
	}
}
