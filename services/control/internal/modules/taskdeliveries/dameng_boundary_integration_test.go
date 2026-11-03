//go:build cgo && odbc_manager && repository_integration && dameng_repository_integration && task_events_native_integration

package taskdeliveries

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This is actual full-schema/driver acceptance source, not a broker test. It
// shares the strict disposable native fixture and does not start NATS. Run it
// against its own fresh schema, separately from the recovery story.
func TestActualDamengTaskSchemaBoundaries(t *testing.T) {
	g := newDamengStory(t)
	n := &nodes.Service{Identity: g.owner, Workspaces: &workspaces.Service{Identity: g.owner}}
	capabilities := wire.Capabilities(wire.SystemdMaintenanceProfile)
	if len(capabilities) != 11 {
		t.Fatal("native schema boundary must be reconciled with the actual maintenance catalog")
	}
	join, e := n.MintForProfile(g.ctx, g.admin, g.workspace, "Synthetic native array boundary", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-boundary-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	enrolled, e := n.Enroll(g.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: capabilities, Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal("actual eleven-capability enrollment must be accepted", e)
	}
	eleven, e := json.Marshal(capabilities)
	if e != nil {
		t.Fatal(e)
	}
	items := make([]any, len(capabilities), len(capabilities)+1)
	for i, capability := range capabilities {
		items[i] = capability
	}
	twelveNull, e := json.Marshal(append(items, nil))
	if e != nil {
		t.Fatal(e)
	}
	twelveValue, e := json.Marshal(append(items, wire.CollectFacts))
	if e != nil {
		t.Fatal(e)
	}
	writeCapabilities := func(raw string) error {
		return g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, "UPDATE managed_nodes SET capabilities=? WHERE id=?", raw, enrolled.NodeID))
			return e // A failed native statement must be the last call in this transaction.
		})
	}
	readCapabilities := func(t *testing.T) string {
		t.Helper()
		var raw string
		if e := g.store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, "SELECT capabilities FROM managed_nodes WHERE id=?", enrolled.NodeID)).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		return raw
	}
	// Empty arrays satisfy the SQL array-length shape, while actual enrollment
	// continues to enforce the negotiated profile's exact nonempty capability set.
	for _, raw := range []string{"[]", string(eleven)} {
		if e := writeCapabilities(raw); e != nil || readCapabilities(t) != raw {
			t.Fatal("valid native array boundary rejected", e)
		}
	}
	for _, test := range []struct{ name, raw string }{
		{"twelfth_null", string(twelveNull)},
		{"twelfth_value", string(twelveValue)},
		{"object", `{}`},
		{"json_null", `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if e := writeCapabilities(test.raw); e == nil {
				t.Fatal("native array type/length guard accepted an invalid value")
			}
			// Fresh connection readback after rollback, never on the failed handle.
			if readCapabilities(t) != string(eleven) {
				t.Fatal("failed native write changed retained capabilities")
			}
		})
	}

	// Native sequence storage must preserve all uint64 values without signed
	// overflow. These are synthetic direct SQL poison coordinates, not evidence
	// that a real broker emitted this sequence or accepted a publication.
	broker := strings.Repeat("a", 64)
	insertCoordinate := func(sequence string) error {
		return g.store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO task_event_dead_letters(id,tenant_id,kind,outcome_unknown,broker_identity,broker_sequence,consumer_name,envelope_sha256,error_code,created_at) VALUES(?,?,'poison',0,?,?,'NativeSchemaBoundary',?,'TASK_EVENT_ENVELOPE_REJECTED',SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)))`, uuid.NewString(), g.tenant, broker, sequence, strings.Repeat("b", 64)))
			return e
		})
	}
	for _, sequence := range []string{"1", "9223372036854775808", "18446744073709551615"} {
		if e := insertCoordinate(sequence); e != nil {
			t.Fatal("canonical uint64 sequence rejected", e)
		}
		if g.count(t, "SELECT count(*) FROM task_event_dead_letters WHERE broker_identity=? AND broker_sequence=? AND consumer_name='NativeSchemaBoundary'", broker, sequence) != 1 {
			t.Fatal("native sequence lost exact decimal identity")
		}
	}
	for _, sequence := range []string{"0", "01", "-1", "18446744073709551616", "1.0", "1e0", "18446744073709551615"} {
		if e := insertCoordinate(sequence); e == nil {
			t.Fatal("invalid or duplicate native broker coordinate accepted")
		}
		if g.count(t, "SELECT count(*) FROM task_event_dead_letters WHERE broker_identity=? AND consumer_name='NativeSchemaBoundary'", broker) != 3 {
			t.Fatal("failed native coordinate mutation changed retained rows")
		}
	}
}
