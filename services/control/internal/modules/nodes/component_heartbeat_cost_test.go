package nodes_test

import (
	"context"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/jackc/pgx/v5/pgxpool"
	"testing"
	"time"
)

func TestComponentHeartbeatCostAtFleetSizes(t *testing.T) {
	const smallFleet, largeFleet = 50, 3000
	small := measureComponentHeartbeats(t, smallFleet, true)
	large := measureComponentHeartbeats(t, largeFleet, false)
	compareObservationCosts(t, []string{"first component report", "component update", "foreign credential"}, small, large)
}

func measureComponentHeartbeats(t *testing.T, fleet int, checkRace bool) map[string]agentcost.Cost {
	t.Helper()
	ctx := t.Context()
	measured := map[string]agentcost.Cost{}
	f := newObservationCostFixture(t, wire.SystemdComponentsProfile, fleet)
	account := f.account
	handler, h, node, client, path := f.handler, f.handshake, f.node, f.client, f.path
	observe := func(name string, heartbeat func()) { measured[name] = f.record(t, heartbeat) }

	h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: h.Facts.ObservedAt}
	client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
	observe("first component report", func() { client.Call("POST", path, h, 200, "DispositionResponse") })
	first := readComponentHeartbeat(t, account.DB, node.NodeID)
	if first.state != "not_installed" || first.heartbeatAt.IsZero() {
		t.Fatal("first report did not persist")
	}

	h.Component.State = "stopped"
	h.Component.Managed = true
	observe("component update", func() { client.Call("POST", path, h, 200, "DispositionResponse") })
	updated := readComponentHeartbeat(t, account.DB, node.NodeID)
	if updated.state != "stopped" {
		t.Fatal("warm report did not update stored state")
	}

	h.Component.State = "unknown"
	h.Component.ErrorCode = "COMPONENT_SYSTEMD_UNAVAILABLE"
	client.Headers["Authorization"] = "Bearer " + security.Token()
	observe("foreign credential", func() {
		if code := client.Problem("POST", path, h, 401); code != "AGENT_UNAUTHORIZED" {
			t.Fatalf("foreign credential returned %s", code)
		}
	})
	if got := readComponentHeartbeat(t, account.DB, node.NodeID); got != updated {
		t.Fatal("refused heartbeat changed node or component state")
	}

	if checkRace {
		credential := replaceCredentialDuringHeartbeat(t, account.DB, handler.Nodes.Service, node, h)
		if got := readComponentHeartbeat(t, account.DB, node.NodeID); got != updated {
			t.Fatal("heartbeat with the replaced credential changed stored state")
		}
		// A disabled node keeps passive reporting authority under its new credential.
		if _, err := account.DB.Exec(ctx, "UPDATE managed_nodes SET status='disabled' WHERE id=$1", node.NodeID); err != nil {
			t.Fatal(err)
		}
		client.Headers["Authorization"] = "Bearer " + credential
		disposition := client.Call("POST", path, h, 200, "DispositionResponse")
		if disposition["status"] != "disabled" || readComponentHeartbeat(t, account.DB, node.NodeID).state != "unknown" {
			t.Fatal("disabled node did not retain passive observation")
		}
	}
	return measured
}

type componentHeartbeatSnapshot struct {
	state, observation      string
	observedAt, heartbeatAt time.Time
}

func readComponentHeartbeat(t *testing.T, db *pgxpool.Pool, node string) componentHeartbeatSnapshot {
	t.Helper()
	var state componentHeartbeatSnapshot
	if err := db.QueryRow(t.Context(), `SELECT c.observed_state,c.observation::text,c.observed_at,n.last_heartbeat_at
		FROM managed_components c JOIN managed_nodes n ON n.id=c.node_id WHERE n.id=$1`, node).
		Scan(&state.state, &state.observation, &state.observedAt, &state.heartbeatAt); err != nil {
		t.Fatal(err)
	}
	return state
}

func replaceCredentialDuringHeartbeat(t *testing.T, db *pgxpool.Pool, service *nodes.Service, node wire.Enrolled, h wire.Handshake) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.WithoutCancel(ctx))
	credential := security.Token()
	var holderPID int32
	if err := holder.QueryRow(ctx, "UPDATE managed_nodes SET credential_hash=$1 WHERE id=$2 RETURNING pg_backend_pid()",
		security.Digest("node-credential:v1:"+credential), node.NodeID).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := service.Heartbeat(ctx, node.NodeID, node.Credential, h)
		done <- err
	}()
	defer func() { cancel(); <-finished }()
	// Confirm discovery saw the old row and the transaction is waiting on
	// the locked node before making the new credential visible.
	wait, cancelWait := context.WithTimeout(ctx, 2*time.Second)
	defer cancelWait()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		if err := db.QueryRow(wait, `SELECT EXISTS(SELECT FROM pg_stat_activity
			WHERE datname=current_database() AND $1=ANY(pg_blocking_pids(pid)))`, holderPID).Scan(&blocked); err != nil {
			t.Fatalf("find heartbeat waiting on the node lock: %v", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("heartbeat finished before the credential transaction committed: %v", err)
		case <-wait.Done():
			t.Fatal("heartbeat did not reach the held node lock")
		case <-poll.C:
		}
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		expected(t, err, "AGENT_UNAUTHORIZED")
	case <-ctx.Done():
		t.Fatal("heartbeat did not finish after credential rotation")
	}
	return credential
}
