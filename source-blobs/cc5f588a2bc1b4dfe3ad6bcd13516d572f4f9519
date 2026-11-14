package audit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func runProductAssistantAuditCommands(t *testing.T, ctx context.Context, store transaction.Store, subject, workspace string) map[string]string {
	t.Helper()
	owner := &identity.Service{Storage: store}
	owner.Config.Workflows.Enabled = true
	spaces := &workspaces.Service{Identity: owner}
	flow := &workflows.Service{Identity: owner, Workspaces: spaces}
	assistant, err := productagent.New(&applications.Service{Identity: owner, Workspaces: spaces, Workflows: flow})
	if err != nil {
		t.Fatal(err)
	}
	var issued identity.IssuedSession
	if err := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var err error
		issued, err = owner.IssueSessionWithin(ctx, q, subject, 1, "portal", "", uuid.NewString(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	actor, err := owner.Authenticate(ctx, issued.Token, "portal")
	if err != nil || actor.Me.Workspace == nil || actor.Me.Workspace.ID != workspace {
		t.Fatalf("authenticate non-default fixture workspace: %v", err)
	}
	subjects := make(map[string]string)
	for _, command := range []struct{ input, action string }{
		{"open keys", "open_keys"},
		{"show applications", "list_applications"},
	} {
		id := uuid.NewString()
		result, err := assistant.RunForRequest(ctx, actor, command.input, "", id)
		if err != nil || result.Command != command.action {
			t.Fatalf("dispatch %s: %v", command.action, err)
		}
		subjects[id] = subject
	}
	return subjects
}

func verifyProductAssistantDisclosure(t *testing.T, ctx context.Context, db *pgxpool.Pool, redacted [][]byte, subjects map[string]string, verify func([][]byte, ...string) ([]byte, error)) {
	t.Helper()
	const strict = "--require-assistant-actors"
	if out, err := verify(redacted, strict); err == nil || !strings.Contains(string(out), `"line":2,"field":"actor_kind"`) {
		t.Fatalf("required disclosure accepted a redacted export: %v: %s", err, out)
	}

	tx, err := db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL ownsolid.audit_disclose_assistant = on"); err != nil {
		t.Fatal(err)
	}
	disclosed := auditExport(t, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if len(disclosed) != len(redacted) {
		t.Fatal("disclosure changed the exported range")
	}
	seen := 0
	target := -1
	for i, row := range disclosed {
		if field(t, row, "hash") != field(t, redacted[i], "hash") ||
			field(t, row, "prev_hash") != field(t, redacted[i], "prev_hash") {
			t.Fatalf("disclosure changed the chain at line %d", i+1)
		}
		if field(t, redacted[i], "actor_kind") != "" || field(t, redacted[i], "actor_id") != "" ||
			bytes.Contains(redacted[i], []byte("fixture-private-metadata")) ||
			bytes.Contains(row, []byte("fixture-private-metadata")) {
			t.Fatalf("export revealed unrequested metadata at line %d", i+1)
		}
		subject, assistant := subjects[field(t, row, "request_id")]
		kind, actorID := field(t, row, "actor_kind"), field(t, row, "actor_id")
		if !assistant {
			if kind != "" || actorID != "" {
				t.Fatalf("disclosed an unrecognized actor at line %d", i+1)
			}
			continue
		}
		if kind != "product_assistant" || actorID != "ownsolid.product-assistant" ||
			field(t, row, "subject_user_id") != subject {
			t.Fatalf("wrong assistant or subject at line %d", i+1)
		}
		if field(t, row, "action") == "product_assistant.open_keys" {
			target = i
		}
		seen++
	}
	if seen != len(subjects) || target < 0 {
		t.Fatal("expected both actual command disclosures")
	}
	if out, err := verify(disclosed, strict); err != nil {
		t.Fatalf("verified assistant export rejected: %v: %s", err, out)
	}

	for _, tc := range []struct{ field, replacement, errorField string }{
		{"actor_kind", "service", "actor_kind"},
		{"actor_id", "fixture-other-actor", "actor_id"},
		{"subject_user_id", uuid.NewString(), "hash"},
		{"metadata_sha256", strings.Repeat("b", 64), "hash"},
	} {
		t.Run("tamper_"+tc.field, func(t *testing.T) {
			var row map[string]*string
			if err := json.Unmarshal(disclosed[target], &row); err != nil {
				t.Fatal(err)
			}
			row[tc.field] = &tc.replacement
			changed := append([][]byte(nil), disclosed...)
			var err error
			changed[target], err = json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(`"line":%d,"field":%q`, target+1, tc.errorField)
			if out, err := verify(changed, strict); err == nil || !strings.Contains(string(out), want) {
				t.Fatalf("tampered disclosure accepted: %v: %s", err, out)
			}
		})
	}
}
