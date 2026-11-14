//go:build aws_integration

package cloudinventory

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This gate requires a separately admitted isolated PostgreSQL fixture. It never
// creates a database/container or calls AWS. All provider responses are synthetic.
func TestAWSActualRepository(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Fatal("AWS_EXPLICIT_ISOLATED_DATABASE_REQUIRED")
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	admin := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	workspace := admin.Me.Workspace.ID
	var scenario atomic.Int32
	var calls atomic.Int32
	var block atomic.Bool
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	provider, connection := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("Action") == "GetCallerIdentity" {
			caller(w)
			return
		}
		calls.Add(1)
		if block.CompareAndSwap(true, false) {
			entered <- struct{}{}
			<-release
		}
		switch scenario.Load() {
		case 1:
			if r.Form.Get("NextToken") == "" {
				fmt.Fprint(w, page("i-0123456789abcdef1", "failure"))
			} else {
				w.WriteHeader(503)
			}
		case 2:
			fmt.Fprint(w, page("", ""))
		default:
			fmt.Fprint(w, page("i-0123456789abcdef0", ""))
		}
	})
	connection.WorkspaceIDs = []string{workspace}
	policy := config.DefaultAWSConfiguration()
	policy.Enabled = true
	policy.MaximumConcurrentImports = 2
	policy.Connections = []config.AWSConnection{connection}
	accounts.Config.AWS = policy
	runtime := config.DefaultRuntimePolicy()
	runtime.HTTP.RequestSeconds = 30
	accounts.Config.Runtime = &runtime
	scopes := &workspaces.Service{DB: accounts.DB, Identity: accounts}
	service := New(accounts, scopes)
	service.Provider = provider
	input := ImportInput{Connection: connection.Name, Region: "us-east-1", Key: uuid.NewString()}
	result, e := service.Import(ctx, admin, workspace, input, uuid.NewString())
	if e != nil || result.Imported != 1 {
		t.Fatal("actual committed import", e)
	}
	originalCalls := calls.Load()
	again := New(accounts, scopes)
	again.Provider = provider
	got, e := again.Import(ctx, admin, workspace, input, uuid.NewString())
	if e != nil || got.Key != result.Key || !got.CompletedAt.Equal(result.CompletedAt) || calls.Load() != originalCalls {
		t.Fatal("restart exact replay before network", e)
	}
	if r, e := again.Receipt(ctx, admin, workspace, input.Key); e != nil || r.Imported != 1 {
		t.Fatal("durable receipt", e)
	}
	if _, e = again.Receipt(ctx, admin, workspace, uuid.NewString()); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("absent receipt", e)
	}
	conflict := input
	conflict.Region = "eu-west-1"
	_, e = again.Import(ctx, admin, workspace, conflict, uuid.NewString())
	wantCode(t, e, "IDEMPOTENCY_CONFLICT")
	check := func(missing bool) {
		t.Helper()
		inventory, e := service.List(ctx, admin, workspace, "", 1)
		if e != nil || len(inventory.Items) != 1 || inventory.Items[0].InstanceID != "i-0123456789abcdef0" || inventory.Items[0].Missing != missing {
			t.Fatal("actual snapshot retained", e, inventory)
		}
	}
	check(false)
	scenario.Store(1)
	failed := input
	failed.Key = uuid.NewString()
	if value, e := service.Import(ctx, admin, workspace, failed, uuid.NewString()); e == nil || value.Key != "" {
		t.Fatal("late-page failure projected success", e)
	}
	check(false)
	_, e = service.Receipt(ctx, admin, workspace, failed.Key)
	wantCode(t, e, "RESOURCE_NOT_FOUND")
	scenario.Store(2)
	if _, e = service.Import(ctx, admin, workspace, ImportInput{connection.Name, "us-east-1", uuid.NewString()}, uuid.NewString()); e != nil {
		t.Fatal("empty complete snapshot", e)
	}
	check(true)
	scenario.Store(0)
	if _, e = accounts.DB.Exec(ctx, `CREATE FUNCTION reject_aws_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='aws.inventory_imported' THEN RAISE EXCEPTION 'synthetic audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_aws_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_aws_audit()`); e != nil {
		t.Fatal(e)
	}
	rollback := ImportInput{connection.Name, "us-east-1", uuid.NewString()}
	if value, e := service.Import(ctx, admin, workspace, rollback, uuid.NewString()); e == nil || value.Key != "" {
		t.Fatal("audit rollback projected result", e)
	}
	check(true)
	if _, e = service.Receipt(ctx, admin, workspace, rollback.Key); e == nil {
		t.Fatal("failed audit persisted receipt")
	}
	if _, e = accounts.DB.Exec(ctx, "DROP TRIGGER reject_aws_audit ON audit_events; DROP FUNCTION reject_aws_audit()"); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Import(ctx, admin, workspace, rollback, uuid.NewString()); e != nil {
		t.Fatal("same-key retry after proven rollback", e)
	}
	check(false)
	if _, e = accounts.DB.Exec(ctx, "UPDATE aws_import_receipts SET imported_count=100 WHERE idempotency_key=$1", rollback.Key); e == nil {
		t.Fatal("receipt mutation accepted")
	}
	accounts.Config.AWS.Enabled = false
	check(false)
	_, e = service.Import(ctx, admin, workspace, ImportInput{connection.Name, "us-east-1", uuid.NewString()}, uuid.NewString())
	wantCode(t, e, "AWS_DISABLED")
	accounts.Config.AWS.Enabled = true
	var nodes int
	if e = accounts.DB.QueryRow(ctx, "SELECT count(*) FROM managed_nodes").Scan(&nodes); e != nil || nodes != 0 {
		t.Fatal("discovery created managed execution identity", e)
	}
	t.Run("current_owner_and_scope", func(t *testing.T) {
		user, password := uuid.NewString(), fixture.Password()
		email := "aws-synthetic+" + user + "@example.invalid"
		invite, e := accounts.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if e = accounts.Register(ctx, fixture.InvitationToken(t, accounts, invite.ID), "AWS synthetic reader", password, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		reader := fixture.Login(t, accounts, email, password, "portal")
		var initialRole string
		var membershipVersion int64
		if e = accounts.DB.QueryRow(ctx, "SELECT role,version FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", admin.Me.Workspace.TenantID, workspace, reader.Me.User.ID).Scan(&initialRole, &membershipVersion); e != nil || initialRole != "member" || membershipVersion != 1 {
			t.Fatal("actual registered default membership", e)
		}
		if changed, e := scopes.ChangeMember(ctx, admin, workspace, reader.Me.User.ID, "viewer", membershipVersion, "Synthetic AWS viewer fixture", uuid.NewString(), false); e != nil || changed.Role != "viewer" || changed.Version != membershipVersion+1 {
			t.Fatal("actual default member changed to viewer", e)
		}
		if _, e = service.List(ctx, reader, workspace, "", 1); e != nil {
			t.Fatal("viewer reads", e)
		}
		before := calls.Load()
		_, e = service.Import(ctx, reader, workspace, ImportInput{connection.Name, "us-east-1", uuid.NewString()}, uuid.NewString())
		wantCode(t, e, "ACCESS_DENIED")
		if calls.Load() != before {
			t.Fatal("unauthorized request reached AWS")
		}
		foreign, e := scopes.Create(ctx, admin, "Synthetic foreign Workspace", admin.Me.User.ID, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = service.List(ctx, reader, foreign.ID, "", 1); e == nil {
			t.Fatal("foreign Workspace read")
		}
		_, e = service.Import(ctx, admin, foreign.ID, ImportInput{connection.Name, "us-east-1", uuid.NewString()}, uuid.NewString())
		wantCode(t, e, "AWS_SCOPE_REJECTED")
		if _, e = accounts.DB.Exec(ctx, "UPDATE workspace_memberships SET role='owner' WHERE workspace_id=$1 AND user_id=$2", workspace, reader.Me.User.ID); e != nil {
			t.Fatal(e)
		}
		scoped, e := service.Import(ctx, reader, workspace, ImportInput{connection.Name, "us-east-1", uuid.NewString()}, uuid.NewString())
		if e != nil {
			t.Fatal("owner import", e)
		}
		_, e = service.Receipt(ctx, admin, workspace, scoped.Key)
		wantCode(t, e, "IDEMPOTENCY_CONFLICT")
		release = make(chan struct{})
		block.Store(true)
		revoked := ImportInput{connection.Name, "us-east-1", uuid.NewString()}
		revokedDone := make(chan error, 1)
		go func() { _, e := service.Import(ctx, reader, workspace, revoked, uuid.NewString()); revokedDone <- e }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("owner provider barrier")
		}
		if _, e = accounts.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", workspace, reader.Me.User.ID); e != nil {
			close(release)
			<-revokedDone
			t.Fatal(e)
		}
		close(release)
		wantCode(t, <-revokedDone, "ACCESS_DENIED")
		if _, e = service.Receipt(ctx, reader, workspace, revoked.Key); e == nil {
			t.Fatal("revoked in-flight import persisted")
		}
		if _, e = accounts.DB.Exec(ctx, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_cloud_revoke' WHERE id=$1", reader.SessionID); e != nil {
			t.Fatal(e)
		}
		_, e = service.List(ctx, reader, workspace, "", 1)
		wantCode(t, e, "AUTH_SESSION_EXPIRED")
	})
	t.Run("network_outside_lock_and_older_snapshot_fence", func(t *testing.T) {
		release = make(chan struct{})
		block.Store(true)
		old := ImportInput{connection.Name, "us-east-1", uuid.NewString()}
		done := make(chan error, 1)
		go func() { _, e := service.Import(ctx, admin, workspace, old, uuid.NewString()); done <- e }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("provider barrier")
		}
		newer := ImportInput{connection.Name, "us-east-1", uuid.NewString()}
		if _, e := service.Import(ctx, admin, workspace, newer, uuid.NewString()); e != nil {
			close(release)
			<-done
			t.Fatal("network held business fence", e)
		}
		close(release)
		wantCode(t, <-done, "AWS_SYNC_SUPERSEDED")
		if _, e := service.Receipt(ctx, admin, workspace, old.Key); e == nil {
			t.Fatal("superseded receipt persisted")
		}
	})
	t.Run("commit_acknowledgement_recovery", func(t *testing.T) {
		store, e := accounts.Repository()
		if e != nil {
			t.Fatal(e)
		}
		uncertain := ImportInput{connection.Name, "us-east-1", uuid.NewString()}
		wrapped := &commitUnknownStore{Store: store, key: uncertain.Key}
		originalDB := accounts.DB
		accounts.Storage, accounts.DB, scopes.DB = wrapped, nil, nil
		defer func() { accounts.Storage = nil; accounts.DB = originalDB; scopes.DB = originalDB }()
		value, e := service.Import(ctx, admin, workspace, uncertain, uuid.NewString())
		wantCode(t, e, "STORAGE_COMMIT_OUTCOME_UNKNOWN")
		if value.Key != "" {
			t.Fatal("unknown commit escaped as success")
		}
		before := calls.Load()
		value, e = service.Import(ctx, admin, workspace, uncertain, uuid.NewString())
		if e != nil || value.Key != uncertain.Key || calls.Load() != before {
			t.Fatal("unknown committed outcome did not reconcile before provider", e)
		}
	})
}

// Real transactions commit first; the target receipt transaction acknowledgement
// is lost. No synthetic persistence, SQL execution or row-return implementation.
type commitUnknownStore struct {
	transaction.Store
	key       string
	delivered bool
}

func (s *commitUnknownStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	committedTarget := false
	e := s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		var count int
		if e := q.QueryRow(ctx, statement(q, "SELECT count(*) FROM aws_import_receipts WHERE idempotency_key=$1", "SELECT count(*) FROM aws_import_receipts WHERE idempotency_key=?", s.key)).Scan(&count); e != nil {
			return e
		}
		committedTarget = count == 1
		return nil
	})
	if e == nil && committedTarget && !s.delivered {
		s.delivered = true
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
