//go:build repository_integration && machine_ingress_integration

package main

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/healthprobe"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const machineHealthUnavailableTable = "machine_health_installation_state"

// This root uses one owned PostgreSQL schema and production serveSelected. It
// never accepts a customer DSN, changes a production table, replaces a readiness
// callback or asserts a synthetic healthy response. The fixture's signed License,
// HTML and node facts retain their explicitly synthetic input scope.
func TestActualMachineHealthReadinessRecoveryComposition(t *testing.T) {
	x := serverCompositionSetup(t, false)
	if x.f.PostgreSQL == nil || x.f.Store.Dialect() != transaction.PostgreSQL {
		t.Fatal("MACHINE_HEALTH_ISOLATED_POSTGRESQL_REQUIRED")
	}
	// Isolate this short explicit DDL fault from the unrelated periodic retention
	// pass. This supported typed test profile changes no production default; the
	// actual startup pass and all production worker ownership remain enabled.
	policy := x.c.Policy()
	policy.Retention.IntervalSeconds = 3600
	if e := policy.Validate(); e != nil {
		t.Fatal(e)
	}
	x.c.Runtime = &policy
	client := miCompositionConfigure(t, x, true)
	miCompositionLicense(t, x)
	password := miCompositionPassword(t, x)
	run := miCompositionStart(t, x, client)
	miCompositionLeaseHeld(t, x)
	admin := miCompositionLogin(t, x, client, x.c.Server.AdminOrigin, "admin", password)
	node, handshake := miCompositionNode(t, x, client, x.c.MachineServer.Origin, admin)
	miCompositionHeartbeat(t, x, client, x.c.MachineServer.Origin, node, handshake)
	before := machineHealthReadback(t, x)
	if before.counts[0] != 1 || before.counts[4] < 1 || before.counts[5] < 1 || before.counts[7] < 1 {
		t.Fatal("meaningful persisted business fixture was not established")
	}
	var schema string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT current_schema()", "")).Scan(&schema); e != nil || !regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`).MatchString(schema) {
		t.Fatal("exact announced disposable PostgreSQL schema required")
	}
	originalOID, unavailableOID, e := machineHealthTableIdentity(x.ctx, x, schema)
	if e != nil || originalOID == "" || unavailableOID != "" {
		t.Fatal("owned installation table identity unavailable or recovery name occupied", e)
	}
	t.Logf("MACHINE_HEALTH_OWNED_INSTALLATION=%s/%s", schema, originalOID)
	input := healthprobe.Input{Audience: "machine", Kind: "ready", DialAddress: x.c.MachineServer.ListenAddress,
		CAFile: x.c.MachineServer.TLSCertificateFile, TimeoutSeconds: 3}
	probe := func(ctx context.Context, kind, expected string) {
		t.Helper()
		selected := input
		selected.Kind = kind
		e := healthprobe.RunWithMachine(ctx, x.c.Server, x.c.MachineServer, selected)
		if expected == "" {
			if e != nil {
				t.Fatal("native verified machine health probe", kind, e)
			}
		} else if e == nil || e.Error() != expected {
			t.Fatal("native machine probe did not preserve the exact failure", kind, e)
		}
	}
	probe(x.ctx, "live", "")
	probe(x.ctx, "ready", "")
	if machineHealthReadback(t, x) != before {
		t.Fatal("healthy native probes changed persisted business state")
	}
	restore := func() error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(x.ctx), 5*time.Second)
		defer cancel()
		original, unavailable, e := machineHealthTableIdentity(ctx, x, schema)
		if e != nil {
			return errors.New("MACHINE_HEALTH_RESTORATION_IDENTITY_UNCONFIRMED")
		}
		if original == originalOID && unavailable == "" {
			return nil
		}
		if original != "" || unavailable != originalOID {
			return errors.New("MACHINE_HEALTH_RESTORATION_IDENTITY_MISMATCH")
		}
		// Inspect actual catalog state before a compensating rename. A failed or
		// unknown first commit is not permission to blindly resend its mutation.
		if e := machineHealthRename(ctx, x, schema, machineHealthUnavailableTable, "installation_state"); e != nil {
			return errors.New("MACHINE_HEALTH_RESTORATION_UNCONFIRMED")
		}
		original, unavailable, e = machineHealthTableIdentity(ctx, x, schema)
		if e != nil || original != originalOID || unavailable != "" {
			return errors.New("MACHINE_HEALTH_RESTORATION_READBACK_FAILED")
		}
		t.Log("MACHINE_HEALTH_INSTALLATION_RESTORED")
		return nil
	}
	// Register before the first DDL, after the running composition cleanup, so
	// failure restores the exact owned relation before worker/store cleanup.
	t.Cleanup(func() {
		if e := restore(); e != nil {
			t.Error("owned installation recovery debt", e)
			t.Logf("MACHINE_HEALTH_RESTORATION_DEBT=%s/%s", schema, originalOID)
		}
	})
	fault, cancelFault := context.WithTimeout(x.ctx, 10*time.Second)
	defer cancelFault()
	if e := machineHealthRename(fault, x, schema, "installation_state", machineHealthUnavailableTable); e != nil {
		t.Fatal("bounded owned installation fault commit unconfirmed", e)
	}
	missing, renamed, e := machineHealthTableIdentity(fault, x, schema)
	if e != nil || missing != "" || renamed != originalOID {
		t.Fatal("owned installation rename was not read back", e)
	}
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodGet, "/health/ready", nil, nil, nil), http.StatusServiceUnavailable, "SERVICE_NOT_READY")
	probe(fault, "ready", "HEALTH_PROBE_RESPONSE_INVALID")
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodGet, "/health/live", nil, nil, nil), http.StatusOK, "")
	probe(fault, "live", "")
	if miCompositionBusinessCounts(t, x) != [4]int{before.counts[0], before.counts[1], before.counts[2], before.counts[3]} {
		t.Fatal("failed readiness or successful liveness changed business counts")
	}
	if e := restore(); e != nil {
		t.Fatal("explicit installation recovery failed", e)
	}
	probe(x.ctx, "ready", "")
	probe(x.ctx, "live", "")
	if machineHealthReadback(t, x) != before {
		t.Fatal("restoration/probes changed immutable installation or business state")
	}
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
	if machineHealthReadback(t, x) != before {
		t.Fatal("same-database owner reopen changed persisted business state")
	}
	run = miCompositionStart(t, x, client)
	probe(x.ctx, "ready", "")
	probe(x.ctx, "live", "")
	miCompositionLeaseHeld(t, x)
	if machineHealthReadback(t, x) != before {
		t.Fatal("same-database production restart or native probes changed business state")
	}
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
	if e := restore(); e != nil || machineHealthReadback(t, x) != before {
		t.Fatal("final same-database recovery/readback failed", e)
	}
	t.Log("actual production machine live/ready through native private-CA probe; exact owned installation-table503/live200/restoration/restart and unchanged business readback; retention interval3600s is an explicit test profile, no installed health scheduling/browser trust/MySQL/target qualification")
}

type machineHealthBusinessState struct {
	counts                 [8]int
	installation, nodeRows string
}

func machineHealthReadback(t *testing.T, x *serverCompositionStory) machineHealthBusinessState {
	t.Helper()
	var state machineHealthBusinessState
	counts := miCompositionBusinessCounts(t, x)
	copy(state.counts[:4], counts[:])
	for index, table := range []string{"users", "sessions", "workspaces", "node_join_tokens"} {
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM "+table, "")).Scan(&state.counts[index+4]); e != nil {
			t.Fatal("actual health business count readback", e)
		}
	}
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT to_jsonb(i)::text FROM installation_state i WHERE id=1", "")).Scan(&state.installation); e != nil {
		t.Fatal("actual installation row readback", e)
	}
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT COALESCE(jsonb_agg(to_jsonb(n) ORDER BY id)::text,'[]') FROM managed_nodes n", "")).Scan(&state.nodeRows); e != nil {
		t.Fatal("actual machine row readback", e)
	}
	return state
}

func machineHealthTableIdentity(ctx context.Context, x *serverCompositionStory, schema string) (string, string, error) {
	var original, unavailable string
	e := x.f.Store.QueryRow(ctx, x.f.Statement("SELECT COALESCE(to_regclass($1)::oid::text,''),COALESCE(to_regclass($2)::oid::text,'')", "",
		pgx.Identifier{schema, "installation_state"}.Sanitize(), pgx.Identifier{schema, machineHealthUnavailableTable}.Sanitize())).Scan(&original, &unavailable)
	return original, unavailable, e
}

func machineHealthRename(ctx context.Context, x *serverCompositionStory, schema, from, to string) error {
	if x.f.Store.Dialect() != transaction.PostgreSQL || !regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`).MatchString(schema) ||
		!(from == "installation_state" && to == machineHealthUnavailableTable || from == machineHealthUnavailableTable && to == "installation_state") {
		return errors.New("MACHINE_HEALTH_OWNED_DDL_REQUIRED")
	}
	return x.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, statement := range []string{"SET LOCAL lock_timeout='1000ms'", "SET LOCAL statement_timeout='3000ms'",
			"ALTER TABLE " + pgx.Identifier{schema, from}.Sanitize() + " RENAME TO " + pgx.Identifier{to}.Sanitize()} {
			if _, e := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, statement)); e != nil {
				return e
			}
		}
		return nil
	})
}
