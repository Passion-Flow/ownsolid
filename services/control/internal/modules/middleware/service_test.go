package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestMutationUnknownCommitReturnsNoProjection(t *testing.T) {
	err := &transaction.Error{Kind: transaction.CommitUnknown}
	out, e := committed(Instance{ID: "must_not_escape", Name: "private"}, normalize(errors.Join(context.Canceled, err)))
	if out.ID != "" || out.Name != "" || e == nil || e.Error() != string(transaction.CommitUnknown) {
		t.Fatal("unknown commit output or classification lost", e)
	}
	op, e := committed(Operation{ID: "must_not_escape", Result: json.RawMessage(`{"proof":"not_committed"}`)}, err)
	if op.ID != "" || op.Result != nil || e == nil {
		t.Fatal("operation error projected tentative output")
	}
}

func TestJoinedCommitUncertaintyPrecedesCancellationAndRollback(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	rolled := &transaction.Error{Kind: transaction.CommitRolledBack}
	cancelled := &transaction.Error{Kind: transaction.Cancelled}
	for _, e := range []error{errors.Join(cancelled, unknown), errors.Join(unknown, cancelled), errors.Join(rolled, unknown), errors.Join(cancelled, rolled, unknown), errors.Join(context.Canceled, cancelled, unknown)} {
		out, got := committed(Instance{ID: "tentative"}, normalize(e))
		if out.ID != "" || got == nil || got.Error() != string(transaction.CommitUnknown) {
			t.Fatal("joined uncertainty lost", got)
		}
	}
	if got := normalize(errors.Join(cancelled, rolled)); got.Error() != string(transaction.CommitRolledBack) {
		t.Fatal("known rollback lost", got)
	}
}

func TestReadProjectionOmitsConnectionAndAuthority(t *testing.T) {
	raw, e := json.Marshal(Instance{ID: "public_id", TenantID: "private_tenant", Fingerprint: "private_fingerprint", ProfileKey: "selected_profile"})
	if e != nil || strings.Contains(string(raw), "private_") {
		t.Fatal("private binding projected", e)
	}
	if normalize(errors.New("password=do_not_expose:vendor message")).Error() != "MIDDLEWARE_STORAGE_UNAVAILABLE" {
		t.Fatal("vendor detail exposed")
	}
}

func TestMissingRepositoryAndFalseRestoreFailBeforeEffects(t *testing.T) {
	s := &Service{}
	if _, e := s.repository(); e == nil {
		t.Fatal("missing selected identity accepted")
	}
	_, e := s.Enqueue(context.Background(), identity.Auth{}, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", OperationInput{Kind: "isolated_restore", Version: 1, Reason: "Synthetic case", IdempotencyKey: "33333333-3333-3333-3333-333333333333", BackupID: "44444444-4444-4444-4444-444444444444", TargetKey: "isolated", ConfirmIsolated: false}, "55555555-5555-5555-5555-555555555555")
	if e == nil || e.Error() != "MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED" {
		t.Fatal("false confirmation reached external authority", e)
	}
}
