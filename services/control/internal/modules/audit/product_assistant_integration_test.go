package audit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestProductAssistantAuditWithin(t *testing.T) {
	db := newAuditFixture(t, auditChainFirstSchema)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	store, err := transaction.FromPostgreSQL(db)
	if err != nil {
		t.Fatal(err)
	}
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	defaultWorkspace, workspace := uuid.NewString(), uuid.NewString()
	foreignWorkspace, disabledWorkspace := uuid.NewString(), uuid.NewString()
	admin, subject, disabledSubject := uuid.NewString(), uuid.NewString(), uuid.NewString()
	seed := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	seed(`INSERT INTO tenants(id,name,status) VALUES($1,'Local','active'),($2,'Foreign','active')`, tenant, foreignTenant)
	seed(`INSERT INTO workspaces(id,tenant_id,name,status)
		VALUES($1,$2,'Default','active'),($3,$2,'Non-default','active'),($4,$5,'Foreign','active'),($6,$2,'Disabled','disabled')`,
		defaultWorkspace, tenant, workspace, foreignWorkspace, foreignTenant, disabledWorkspace)
	seed(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES
		($1,'admin@example.invalid','admin@example.invalid','Admin','en','active',true),
		($2,'subject@example.invalid','subject@example.invalid','Subject','en','active',false),
		($3,'disabled@example.invalid','disabled@example.invalid','Disabled','en','disabled',false)`, admin, subject, disabledSubject)
	seed(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role)
		VALUES($1,$2,$3,'member'),($1,$4,$5,'member'),($1,$2,$6,'member'),($7,$2,$3,'member')`,
		subject, tenant, workspace, foreignTenant, foreignWorkspace, disabledWorkspace, disabledSubject)
	seed(`UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,
		primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1`, tenant, defaultWorkspace, admin)

	err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return audit.WriteWithin(ctx, q, admin, subject, "admin", "fixture.human", "success", "", "", "", uuid.NewString())
	})
	if err != nil {
		t.Fatal(err)
	}
	var legacyHash string
	if err := db.QueryRow(ctx, "SELECT encode(hash,'hex') FROM audit_chain_head").Scan(&legacyHash); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	event := audit.ProductAssistantEvent{
		SubjectUserID: subject, TenantID: tenant, WorkspaceID: workspace, Audience: "portal",
		RequestID: uuid.NewString(), ToolID: "list_applications", Outcome: "success",
	}
	appendEvent := func(event audit.ProductAssistantEvent) error {
		return store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return audit.WriteProductAssistantWithin(ctx, q, event)
		})
	}
	var schema string
	if err := db.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{"audit_fixture_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	err = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, statement := range []string{
			"CREATE ROLE " + role + " NOLOGIN",
			"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role,
			"GRANT SELECT ON cells, workspaces, installation_state, tenants, users, workspace_memberships TO " + role,
			"GRANT INSERT ON audit_events TO " + role,
			"SET LOCAL ROLE " + role,
		} {
			if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, statement)); err != nil {
				return err
			}
		}
		if err := audit.WriteProductAssistantWithin(ctx, q, event); err != nil {
			return err
		}
		for _, statement := range []string{"RESET ROLE", "DROP OWNED BY " + role, "DROP ROLE " + role} {
			if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, statement)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("assistant append with serving privileges: %v", err)
	}
	var matches bool
	err = db.QueryRow(ctx, `SELECT actor_user_id IS NULL AND subject_user_id=$2 AND tenant_id=$3 AND workspace_id=$4
		AND audience='portal' AND action='product_assistant.list_applications' AND outcome='success'
		AND reason IS NULL AND identifier_digest IS NULL
		AND metadata='{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}'::jsonb
		FROM audit_events WHERE request_id=$1`, event.RequestID, subject, tenant, workspace).Scan(&matches)
	if err != nil || !matches {
		t.Fatalf("unexpected assistant attribution: %v", err)
	}
	adminEvent := event
	adminEvent.SubjectUserID, adminEvent.Audience, adminEvent.ToolID = admin, "admin", "open_home"
	adminEvent.RequestID = uuid.NewString()
	if err := appendEvent(adminEvent); err != nil {
		t.Fatalf("authorized platform administrator without a workspace membership: %v", err)
	}

	for _, tc := range []struct {
		name      string
		tenant    string
		workspace string
		audience  string
		subject   string
	}{
		{"tenant_mismatch", foreignTenant, workspace, "portal", subject},
		{"foreign_membership", foreignTenant, foreignWorkspace, "portal", subject},
		{"unrelated_workspace", tenant, defaultWorkspace, "portal", subject},
		{"unauthorized_admin", tenant, workspace, "admin", subject},
		{"disabled_workspace", tenant, disabledWorkspace, "portal", subject},
		{"disabled_subject", tenant, workspace, "portal", disabledSubject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denied := event
			denied.TenantID, denied.WorkspaceID, denied.Audience = tc.tenant, tc.workspace, tc.audience
			denied.SubjectUserID, denied.RequestID = tc.subject, uuid.NewString()
			if err := appendEvent(denied); !errors.Is(err, audit.ErrProductAssistantScopeDenied) {
				t.Fatalf("scope denial expected, got %v", err)
			}
		})
	}
	invalid := event
	invalid.ToolID = "command text"
	if err := appendEvent(invalid); !errors.Is(err, audit.ErrProductAssistantEventInvalid) {
		t.Fatalf("invalid tool ID accepted: %v", err)
	}
	if err := audit.WriteProductAssistantWithin(ctx, store, event); err == nil || err.Error() != "AUDIT_TRANSACTION_REQUIRED" {
		t.Fatalf("pool executor accepted: %v", err)
	}

	// Force a native insert error after a business write, then exercise an
	// independent caller rollback after a successful append.
	cancelled := errors.New("fixture cancelled after append")
	rollbackEvent := event
	rollbackEvent.RequestID = uuid.NewString()
	rollbackEvent.Outcome = "accepted"
	for _, failInsert := range []bool{true, false} {
		err := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL,
				"UPDATE workspaces SET name='Uncommitted' WHERE id=$1", workspace)); err != nil {
				return err
			}
			if failInsert {
				if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `ALTER TABLE audit_events
					ADD CONSTRAINT fixture_refuses_assistant CHECK (outcome <> 'accepted')`)); err != nil {
					t.Fatal(err)
				}
			}
			if err := audit.WriteProductAssistantWithin(ctx, q, rollbackEvent); err != nil {
				return err
			}
			return cancelled
		})
		if failInsert {
			var dbError *transaction.Error
			if !errors.As(err, &dbError) || dbError.Kind != transaction.CheckConflict {
				t.Fatalf("expected a PostgreSQL write failure, got %v", err)
			}
		} else if !errors.Is(err, cancelled) {
			t.Fatalf("caller rollback failed: %v", err)
		}
		var name string
		if err := db.QueryRow(ctx, "SELECT name FROM workspaces WHERE id=$1", workspace).Scan(&name); err != nil || name != "Non-default" {
			t.Fatalf("business write survived rollback: %v", err)
		}
	}

	subjects := runProductAssistantAuditCommands(t, ctx, store, subject, workspace)
	subjects[event.RequestID] = subject
	subjects[adminEvent.RequestID] = admin
	seed(`INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id,tenant_id,workspace_id,subject_user_id,metadata)
		VALUES($1,clock_timestamp(),'portal','product_assistant.fixture_redacted','success',$2,$3,$4,$5,
		'{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant","extra":"fixture-private-metadata"}'::jsonb)`,
		uuid.NewString(), uuid.NewString(), tenant, workspace, subject)

	lines := auditExport(t, db)
	if len(lines) != 7 || field(t, lines[0], "format") != "ownsolid.audit.v1" ||
		field(t, lines[0], "actor_user_id") != admin || field(t, lines[0], "workspace_id") != defaultWorkspace ||
		field(t, lines[0], "hash") != legacyHash || field(t, lines[1], "prev_hash") != legacyHash ||
		field(t, lines[1], "format") != "ownsolid.audit.v2" || field(t, lines[1], "workspace_id") != workspace ||
		field(t, lines[1], "request_id") != event.RequestID || bytes.Contains(lines[1], []byte("ownsolid.product-assistant")) {
		t.Fatal("unexpected chain history, scope, correlation or metadata disclosure")
	}
	var position int
	var checkpoint string
	err = db.QueryRow(ctx, "SELECT chain_position,encode(hash,'hex') FROM audit_chain_head").Scan(&position, &checkpoint)
	if err != nil || position != len(lines) || checkpoint != field(t, lines[len(lines)-1], "hash") {
		t.Fatalf("audit tail diverged from committed events: %v", err)
	}
	binary := os.Getenv("OWNSOLID_TEST_AUDIT_VERIFY")
	if binary == "" {
		t.Log("set OWNSOLID_TEST_AUDIT_VERIFY to test the built standalone executable")
		return
	}
	verify := func(input [][]byte, flags ...string) ([]byte, error) {
		args := []string{"--file", "-", "--output", "json", "--expected-last-hash", checkpoint}
		cmd := exec.CommandContext(ctx, binary, append(args, flags...)...)
		cmd.Stdin = bytes.NewReader(bytes.Join(input, []byte("\n")))
		return cmd.CombinedOutput()
	}
	if out, err := verify(lines); err != nil {
		t.Fatalf("mixed chain rejected: %v: %s", err, out)
	}
	verifyProductAssistantDisclosure(t, ctx, db, lines, subjects, verify)
	seed(`UPDATE audit_events SET metadata=jsonb_set(metadata,'{actor_id}','"fixture-other-actor"'::jsonb)
		WHERE request_id=$1`, event.RequestID)
	if out, err := verify(auditExport(t, db)); err == nil || !strings.Contains(string(out), `"line":2,"field":"hash"`) {
		t.Fatalf("changed actor metadata accepted: %v: %s", err, out)
	}
}
