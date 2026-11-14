//go:build repository_integration && middleware_endpoint_integration

package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This joins the actual TLS endpoint to the actual selected repository/worker;
// it makes no native backup, full server or browser qualification claim.
func TestActualMiddlewarePostgreSQLWorkflow(t *testing.T) {
	_, configuration, original, _ := endpointInputs(t)
	p := pairedSetup(t)
	p.configuration = configuration
	var e error
	p.s, e = New(p.accounts, &workspaces.Service{Identity: p.accounts}, configuration)
	if e != nil {
		t.Fatal(e)
	}
	instance, _ := p.register(t)
	input := OperationInput{Kind: "health", Reason: "Actual PostgreSQL TLS worker observation", Version: instance.Version, IdempotencyKey: uuid.NewString()}
	operation, e := p.s.Enqueue(p.ctx, p.owner, p.f.Workspace, instance.ID, input, uuid.NewString())
	if e != nil {
		t.Fatal("actual health admission", e)
	}
	if e = p.s.Pass(p.ctx); e != nil {
		t.Fatal("actual worker pass", e)
	}
	reopened, e := New(p.accounts, &workspaces.Service{Identity: p.accounts}, configuration)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := reopened.Operation(p.ctx, p.owner, p.f.Workspace, operation.ID)
	var result executionEvidence
	if e != nil || stored.State != "succeeded" || json.Unmarshal(stored.Result, &result) != nil || result.Kind != "health" || result.Observation == nil || !result.Observation.valid() || result.MySQLObservation != nil {
		t.Fatal("actual durable PostgreSQL health evidence missing", e)
	}
	if result.Observation.Database != original.profile.Database || result.Observation.User != original.profile.User || result.Observation.WriteAnyTable || result.Observation.Superuser || result.Observation.CreateAnySchemaObjects {
		t.Fatal("actual endpoint facts changed")
	}
	if e = reopened.VerifyStored(p.ctx); e != nil {
		t.Fatal("actual reopened integrity", e)
	}
	replay, e := reopened.Enqueue(p.ctx, p.owner, p.f.Workspace, instance.ID, input, uuid.NewString())
	if e != nil || replay.ID != operation.ID || !bytes.Equal(replay.Result, stored.Result) {
		t.Fatal("exact original health replay changed", e)
	}
	if n := p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.operation_succeeded' AND target_id=$1", "SELECT count(*) FROM audit_events WHERE action='middleware.operation_succeeded' AND target_id=?", operation.ID); n != 1 {
		t.Fatal("terminal audit repeated")
	}
	var evidence map[string]json.RawMessage
	_ = json.Unmarshal(stored.Result, &evidence)
	var facts map[string]json.RawMessage
	_ = json.Unmarshal(evidence["observation"], &facts)
	delete(facts, "superuser")
	evidence["observation"], _ = json.Marshal(facts)
	corrupt, _ := json.Marshal(evidence)
	_, mutationError := p.f.Store.Exec(p.ctx, p.f.Statement("UPDATE middleware_operations SET result_json=$1,result_sha256=$2 WHERE id=$3", "UPDATE middleware_operations SET result_json=?,result_sha256=? WHERE id=?", string(corrupt), hash(corrupt), operation.ID))
	var rejected *transaction.Error
	wantState := "P0001"
	if p.f.Store.Dialect() == transaction.MySQL {
		wantState = "45000"
	}
	if !errors.As(mutationError, &rejected) || rejected.SQLState != wantState {
		t.Fatal("actual terminal trigger failed to refuse replacement", mutationError)
	}
	unchanged, e := reopened.Operation(p.ctx, p.admin, p.f.Workspace, operation.ID)
	if e != nil || !bytes.Equal(unchanged.Result, stored.Result) {
		t.Fatal("refused terminal replacement changed evidence", e)
	}
	if e = reopened.VerifyStored(p.ctx); e != nil {
		t.Fatal("retained evidence integrity", e)
	}
}
