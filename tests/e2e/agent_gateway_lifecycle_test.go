package e2e

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type gatewayRetirement struct {
	reason     string
	ageSeconds float64
	forced     bool
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
	state := gatewayConnectionState{active: map[string]string{}, retired: map[string]gatewayRetirement{}}
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "agent_gateway_started") {
			state = gatewayConnectionState{active: map[string]string{}, retired: map[string]gatewayRetirement{}}
			continue
		}
		var entry struct {
			Message      string   `json:"msg"`
			Node         string   `json:"node_id"`
			ConnectionID string   `json:"connection_id"`
			Reason       string   `json:"reason"`
			AgeSeconds   *float64 `json:"age_seconds"`
			Forced       *bool    `json:"forced"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			for _, message := range []string{"agent_session_opened", "agent_session_closed", "http_connection_retired"} {
				if strings.Contains(line, message) {
					entry.Message = message
					break
				}
			}
			for _, field := range strings.Fields(line) {
				key, value, _ := strings.Cut(field, "=")
				value = strings.Trim(value, `"`)
				switch key {
				case "node_id":
					entry.Node = value
				case "connection_id":
					entry.ConnectionID = value
				case "reason":
					entry.Reason = value
				case "age_seconds":
					number, err := strconv.ParseFloat(value, 64)
					if err != nil {
						t.Fatalf("invalid gateway age observation: %v", err)
					}
					entry.AgeSeconds = &number
				case "forced":
					forced, err := strconv.ParseBool(value)
					if err != nil {
						t.Fatalf("invalid gateway closure observation: %v", err)
					}
					entry.Forced = &forced
				}
			}
		}
		switch entry.Message {
		case "agent_session_opened":
			if entry.Node != "" {
				state.active[entry.Node] = entry.ConnectionID
			}
		case "agent_session_closed":
			if entry.ConnectionID == "" || state.active[entry.Node] == entry.ConnectionID {
				delete(state.active, entry.Node)
			}
		case "http_connection_retired":
			if entry.ConnectionID == "" || entry.AgeSeconds == nil || entry.Forced == nil {
				t.Fatal("gateway retirement omitted its transport identity or outcome")
			}
			state.retired[entry.ConnectionID] = gatewayRetirement{entry.Reason, *entry.AgeSeconds, *entry.Forced}
		}
	}
	return state
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
				if retirement.reason != "connection_age" || retirement.forced ||
					retirement.ageSeconds < 0.9*maximumAgeSeconds || retirement.ageSeconds > maximumAgeSeconds {
					t.Fatalf("invalid automatic retirement: %+v", retirement)
				}
			}
		}
		return true
	})
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
