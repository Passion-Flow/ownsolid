//go:build plugin_database_integration && knowledge_encoder_integration

package workflows_test

import (
	"context"
	"testing"
	"time"

	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
)

// The caller already proved this pool has only scoped DML and no schema or
// migration authority, and executed the real signed-tool run through that pool.
func verifyPortalRestrictedRuntime(t *testing.T, h *identityhttp.Handler, a identity.Auth, workflow, run, outcome string) {
	t.Helper()
	ctx := context.Background()
	s := h.Notifications.Service
	if e := s.Start(ctx); e != nil {
		t.Fatal("DML-only notice maintenance start", e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := s.Stop(ctx); e != nil {
			t.Error(e)
		}
	}()
	for _, statement := range []string{"ALTER TABLE user_preferences ADD COLUMN forbidden integer", "TRUNCATE terminal_notifications"} {
		_, e := h.Service.DB.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Code != "42501" {
			t.Fatal("Portal runtime DDL/TRUNCATE not refused", e)
		}
	}
	before, e := h.Preferences.Service.Get(ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	theme := preferences.DarkTheme
	if before.Theme == theme {
		theme = preferences.LightTheme
	}
	changed, e := h.Preferences.Service.Update(ctx, a, preferences.UpdateRequest{Theme: &theme, ExpectedRevision: before.Revision}, uuid.NewString())
	if e != nil || changed.Revision != before.Revision+1 || changed.Theme != theme {
		t.Fatal("DML-only preference update", e)
	}
	persisted, e := h.Preferences.Service.Get(ctx, a)
	if e != nil || persisted != changed {
		t.Fatal("DML-only preference persistence", e)
	}
	apps, e := h.Applications.Service.List(ctx, a, a.Me.Workspace.ID, url.Values{})
	if e != nil || len(apps.Items) != 1 || apps.Items[0].WorkflowID != workflow {
		t.Fatal("DML-only published projection", e)
	}
	page, e := s.List(ctx, a, url.Values{})
	if e != nil {
		t.Fatal(e)
	}
	id := ""
	for _, notice := range page.Items {
		if notice.RunID == run {
			id = notice.ID
			if notice.Outcome != outcome {
				t.Fatal("DML-only real terminal outcome")
			}
		}
	}
	if id == "" {
		t.Fatal("DML-only terminal trigger did not persist notice")
	}
	n, e := s.Read(ctx, a, id, uuid.NewString())
	if e != nil || n.ReadAt == nil {
		t.Fatal("DML-only read/audit", e)
	}
	again, e := s.Read(ctx, a, id, uuid.NewString())
	if e != nil || again.ReadAt == nil || !again.ReadAt.Equal(*n.ReadAt) {
		t.Fatal("DML-only repeated read", e)
	}

	if e = h.Preferences.Service.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
}
