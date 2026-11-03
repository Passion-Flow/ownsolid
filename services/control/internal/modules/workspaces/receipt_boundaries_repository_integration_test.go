//go:build repository_integration

package workspaces_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// Explicit test-only barriers delegate every SQL result and actual transaction.
// They do not represent network faults or fabricate successful repository rows.
type heldReceiptWrite struct {
	transaction.Store
	ready    chan struct{}
	release  chan struct{}
	rollback bool
}

func (p *heldReceiptWrite) Within(ctx context.Context, readOnly bool, operation func(context.Context, transaction.Executor) error) error {
	return p.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		if e := operation(ctx, q); e != nil {
			return e
		}
		close(p.ready)
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if p.rollback {
			return errors.New("SYNTHETIC_RECEIPT_TRANSACTION_ROLLBACK")
		}
		return nil
	})
}

type receiptReadEntered struct {
	transaction.Store
	entered chan struct{}
}
type receiptReadExecutor struct {
	transaction.Executor
	entered chan struct{}
	once    sync.Once
}

func (p *receiptReadEntered) Within(ctx context.Context, readOnly bool, operation func(context.Context, transaction.Executor) error) error {
	return p.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return operation(ctx, &receiptReadExecutor{Executor: q, entered: p.entered})
	})
}
func (p *receiptReadExecutor) QueryRow(ctx context.Context, statement transaction.Statement) transaction.Row {
	p.once.Do(func() { close(p.entered) })
	return p.Executor.QueryRow(ctx, statement)
}

func TestActualWorkspaceReceiptBoundaries(t *testing.T) {
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	accounts := &identity.Service{Storage: f.Store}
	s := &workspaces.Service{Identity: accounts}
	var issued identity.IssuedSession
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = accounts.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := accounts.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	exec := func(t *testing.T, pg, my string, args ...any) {
		t.Helper()
		if _, e := f.Store.Exec(ctx, f.Statement(pg, my, args...)); e != nil {
			t.Fatal(e)
		}
	}
	missing := func(t *testing.T, request string) {
		t.Helper()
		out, e := s.Operation(ctx, a, request)
		if e != nil || out.State != workspaces.OperationUnconfirmed || out.RequestID != request || out.Event != nil {
			t.Fatal("receipt absence/isolation", out, e)
		}
	}
	appendAudit := func(t *testing.T, request, tenant string, workspace any, action, outcome, kind, target string) {
		t.Helper()
		exec(t,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin',$5,$6,$7,$8,$9)`,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin',?,?,?,?,?)`, uuid.NewString(), f.User, tenant, workspace, action, outcome, kind, target, request)
	}

	t.Run("foreign_tenant_unrelated_and_failed_events_never_confirm", func(t *testing.T) {
		tenant, workspace, request := uuid.NewString(), uuid.NewString(), uuid.NewString()
		exec(t, "INSERT INTO tenants(id,name,status) VALUES($1,'Receipt foreign tenant','active')", "INSERT INTO tenants(id,name,status) VALUES(?,'Receipt foreign tenant','active')", tenant)
		exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Receipt foreign Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Receipt foreign Workspace','active')", workspace, tenant)
		appendAudit(t, request, tenant, workspace, "workspace.updated", "success", "workspace", workspace)
		missing(t, request)
		request = uuid.NewString()
		appendAudit(t, request, f.Tenant, f.Workspace, "user.updated", "success", "workspace", f.Workspace)
		missing(t, request)
		request = uuid.NewString()
		appendAudit(t, request, f.Tenant, f.Workspace, "workspace.updated", "failure", "workspace", f.Workspace)
		missing(t, request)
	})
	t.Run("mismatched_target_and_null_scope_fail_without_partial_receipt", func(t *testing.T) {
		for _, fixture := range []struct {
			workspace any
			kind      string
		}{{f.Workspace, "workspace_note"}, {nil, "workspace"}} {
			request := uuid.NewString()
			appendAudit(t, request, f.Tenant, fixture.workspace, "workspace.updated", "success", fixture.kind, f.Workspace)
			out, e := s.Operation(ctx, a, request)
			if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || out != (workspaces.OperationReceipt{}) {
				t.Fatal("corrupt partial receipt", out, e)
			}
		}
	})
	for _, rollback := range []bool{false, true} {
		name := "inflight_real_commit"
		if rollback {
			name = "inflight_real_rollback"
		}
		t.Run(name, func(t *testing.T) {
			request := uuid.NewString()
			held := &heldReceiptWrite{Store: f.Store, ready: make(chan struct{}), release: make(chan struct{}), rollback: rollback}
			var release sync.Once
			defer release.Do(func() { close(held.release) })
			writer := &workspaces.Service{Identity: &identity.Service{Storage: held}}
			type result struct {
				value workspaces.Workspace
				err   error
			}
			written := make(chan result, 1)
			go func() {
				value, e := writer.Create(ctx, a, "Inflight receipt "+uuid.NewString(), f.User, request)
				written <- result{value, e}
			}()
			select {
			case <-held.ready:
			case <-ctx.Done():
				t.Fatal("actual domain/audit SQL barrier missing")
			}
			entered := make(chan struct{})
			reader := &workspaces.Service{Identity: &identity.Service{Storage: &receiptReadEntered{Store: f.Store, entered: entered}}}
			type readResult struct {
				value workspaces.OperationReceipt
				err   error
			}
			read := make(chan readResult, 1)
			go func() { value, e := reader.Operation(ctx, a, request); read <- readResult{value, e} }()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("actual receipt authorization read not entered")
			}
			select {
			case value := <-read:
				t.Fatal("inflight receipt returned before actual outcome", value.err)
			case <-time.After(50 * time.Millisecond):
			}
			release.Do(func() { close(held.release) })
			var w result
			select {
			case w = <-written:
			case <-ctx.Done():
				t.Fatal("actual write completion timeout")
			}
			var receipt readResult
			select {
			case receipt = <-read:
			case <-ctx.Done():
				t.Fatal("actual receipt completion timeout")
			}
			if receipt.err != nil {
				t.Fatal(receipt.err)
			}
			if rollback {
				if w.err == nil || w.err.Error() != "SYNTHETIC_RECEIPT_TRANSACTION_ROLLBACK" || w.value != (workspaces.Workspace{}) || receipt.value.State != workspaces.OperationUnconfirmed || receipt.value.Event != nil {
					t.Fatal("rollback reconciliation", w.err, receipt.value)
				}
			} else if w.err != nil || w.value.ID == "" || receipt.value.State != workspaces.OperationCommitted || receipt.value.Event == nil || receipt.value.Event.WorkspaceID != w.value.ID {
				t.Fatal("commit reconciliation", w.err, receipt.value)
			}
		})
	}
	t.Run("privileged_owned_audit_purge_does_not_imply_domain_rollback", func(t *testing.T) {
		request := uuid.NewString()
		w, e := s.Create(ctx, a, "Synthetic audit retention", f.User, request)
		if e != nil {
			t.Fatal(e)
		}
		before, e := s.Operation(ctx, a, request)
		if e != nil || before.State != workspaces.OperationCommitted {
			t.Fatal(before, e)
		}
		// The fixture is exclusively disposable and its operator owns DDL. This
		// privileged fault introduces no product/customer audit deletion command.
		exec(t, "TRUNCATE TABLE audit_events", "TRUNCATE TABLE audit_events")
		missing(t, request)
		current, e := s.Get(ctx, a, w.ID, false)
		if e != nil || current.ID != w.ID {
			t.Fatal("purge was confused with domain rollback", e)
		}
	})
}
