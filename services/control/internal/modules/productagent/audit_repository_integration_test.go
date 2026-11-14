//go:build repository_integration

package productagent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	applicationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

type auditHead struct {
	position int64
	hash     string
}

func readAuditHead(t *testing.T, ctx context.Context, db *pgxpool.Pool) auditHead {
	t.Helper()
	var head auditHead
	if err := db.QueryRow(ctx, "SELECT chain_position, encode(hash, 'hex') FROM audit_chain_head").Scan(&head.position, &head.hash); err != nil {
		t.Fatal(err)
	}
	return head
}

func verifyScopedCommandAudit(t *testing.T, ctx context.Context, db *pgxpool.Pool, service *Service, apps *applications.Service, admin, portal identity.Auth) {
	t.Helper()
	cases := []struct {
		actor identity.Auth
		input string
		id    string
	}{
		{admin, "  OPEN NODES  ", "open_nodes"},
		{portal, "\u6253\u5f00\u5bc6\u94a5", "open_keys"},
		{portal, "show applications", listApplications},
		{portal, "show conversations", listConversations},
	}

	for _, command := range cases {
		t.Run("audit/"+command.id, func(t *testing.T) {
			requestID := uuid.NewString()
			result, err := service.RunForRequest(ctx, command.actor, command.input, "", requestID)
			if err != nil || result.Command != command.id {
				t.Fatalf("audited command: %#v, %v", result, err)
			}
			var count, attributed int
			err = db.QueryRow(ctx, `
SELECT count(*), count(*) FILTER (WHERE
    actor_user_id IS NULL AND subject_user_id=$2 AND tenant_id=$3 AND workspace_id=$4
    AND audience=$5 AND action=$6 AND outcome='success'
    AND metadata='{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}'::jsonb
    AND reason IS NULL AND target_type IS NULL AND target_id IS NULL AND identifier_digest IS NULL
    AND chain_position IS NOT NULL)
FROM audit_events WHERE request_id=$1`,
				requestID, command.actor.Me.User.ID, portal.Me.Workspace.TenantID, portal.Me.Workspace.ID,
				command.actor.Me.Audience, "product_assistant."+command.id).Scan(&count, &attributed)
			if err != nil || count != 1 || attributed != 1 {
				t.Fatalf("expected one attributed metadata-only event, got %d events and %d matches: %v", count, attributed, err)
			}
		})
	}

	before := readAuditHead(t, ctx, db)
	if result, err := service.Run(ctx, admin, "open nodes", ""); err != nil || result.Path != "/nodes" {
		t.Fatalf("in-process navigation: %#v, %v", result, err)
	}
	var generatedID string
	if err := db.QueryRow(ctx, `SELECT request_id::text FROM audit_events WHERE chain_position=$1 AND action='product_assistant.open_nodes'`,
		before.position+1).Scan(&generatedID); err != nil {
		t.Fatal(err)
	}
	if id, err := uuid.Parse(generatedID); err != nil || id == uuid.Nil {
		t.Fatalf("in-process command did not assign a server request ID: %v", err)
	}

	exec := func(t *testing.T, statement string) {
		t.Helper()
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec(t, `CREATE FUNCTION reject_assistant_audit_fixture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'fixture private audit detail' USING ERRCODE = '23514'; END
$$`)
	for _, fault := range []struct {
		name   string
		create string
		drop   string
	}{
		{
			"append",
			`CREATE TRIGGER reject_assistant_fixture BEFORE INSERT ON audit_events FOR EACH ROW
WHEN (NEW.action LIKE 'product_assistant.%') EXECUTE FUNCTION reject_assistant_audit_fixture()`,
			"DROP TRIGGER reject_assistant_fixture ON audit_events",
		},
		{
			"commit",
			`CREATE CONSTRAINT TRIGGER reject_assistant_fixture AFTER UPDATE ON audit_chain_head
DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_assistant_audit_fixture()`,
			"DROP TRIGGER reject_assistant_fixture ON audit_chain_head",
		},
	} {
		t.Run("rollback/"+fault.name, func(t *testing.T) {
			exec(t, fault.create)
			defer exec(t, fault.drop)
			before := readAuditHead(t, ctx, db)
			for _, command := range cases {
				requestID := uuid.NewString()
				result, err := service.RunForRequest(ctx, command.actor, command.input, "", requestID)
				if err == nil || result != (Result{}) || strings.Contains(err.Error(), "fixture private audit detail") {
					t.Errorf("%s released a result or private diagnostics after audit refusal: %#v, %v", command.id, result, err)
				}
				var count int
				if err := db.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE request_id=$1", requestID).Scan(&count); err != nil || count != 0 {
					t.Errorf("%s left %d audit events after refusal: %v", command.id, count, err)
				}
			}
			handler := &applicationhttp.Handler{Service: apps}
			for _, path := range []string{"/api/v1/applications", "/api/v1/application-conversations"} {
				request := httptest.NewRequestWithContext(ctx, http.MethodGet, path+"?workspace_id="+portal.Me.Workspace.ID, nil)
				page, status, err := handler.Handle(request, portal, uuid.NewString())
				if status != http.StatusOK || err != nil {
					t.Errorf("ordinary projection %s changed with the assistant audit refusal: %d, %v", path, status, err)
					continue
				}
				switch page := page.(type) {
				case applications.Page[applications.Application]:
					if len(page.Items) != 1 || page.NextCursor == nil || page.Items[0].WorkspaceID != portal.Me.Workspace.ID {
						t.Errorf("ordinary application page changed: %#v", page)
					}
				case applications.Page[applications.Conversation]:
					if len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].WorkspaceID != portal.Me.Workspace.ID {
						t.Errorf("ordinary conversation page changed: %#v", page)
					}
				default:
					t.Errorf("ordinary projection %s returned %T", path, page)
				}
			}
			if after := readAuditHead(t, ctx, db); after != before {
				t.Errorf("failed assistant commands or ordinary reads changed the audit head: %#v -> %#v", before, after)
			}
		})
	}
}
