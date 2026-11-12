package http

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/jackc/pgx/v5"
)

func TestPostgresHTTPWorkspaceQuotaStatus(t *testing.T) {
	accounts := identityfixture.New(t)
	password := identityfixture.Bootstrap(t, accounts)
	admin := identityfixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	portal := identityfixture.Login(t, accounts, "admin@example.invalid", password, "portal")
	for _, file := range []string{"../testdata/quota.sql", "../testdata/reservations.sql"} {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := accounts.DB.Exec(t.Context(), string(body)); err != nil {
			t.Fatal(err)
		}
	}
	h := &Handler{Workspaces: &workspaces.Service{DB: accounts.DB, Identity: accounts}}
	scope := portal.Me.Workspace
	paths := []string{"/api/v1/admin/scheduling/quota?workspace_id=" + scope.ID,
		"/api/v1/workspaces/" + scope.ID + "/scheduling/quota"}
	actors := []identity.Auth{admin, portal}
	read := func(actor identity.Auth, path string) scheduling.WorkspaceQuotaStatus {
		t.Helper()
		w := httptest.NewRecorder()
		value, status, err := h.Handle(w, httptest.NewRequest("GET", path, nil).WithContext(t.Context()), actor, "")
		if err != nil || status != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("authenticated handler: %+v, %d, %v", value, status, err)
		}
		out, ok := value.(scheduling.WorkspaceQuotaStatus)
		if !ok || out.WorkspaceID != scope.ID {
			t.Fatalf("incorrect public subject: %+v", value)
		}
		return out
	}
	for i, actor := range actors {
		out := read(actor, paths[i])
		if out.State != scheduling.WorkspaceQuotaUnconfigured || out.Reason == nil ||
			out.Reason.Action != scheduling.ConfigureQuotaPolicy || out.Resources != nil {
			t.Fatalf("missing policy became capacity: %+v", out)
		}
	}
	limit := scheduling.Resources{scheduling.CPU: 2000, scheduling.NVIDIAGPU: 1}
	policy := []scheduling.QuotaNode{
		{ID: "platform", Level: scheduling.PlatformQuota, Limits: scheduling.Quota{Guaranteed: limit, Burst: limit}},
		{ID: scope.TenantID, ParentID: "platform", TenantID: scope.TenantID, Level: scheduling.TenantQuota, Limits: scheduling.Quota{Guaranteed: limit, Burst: limit}},
		{ID: "department", ParentID: scope.TenantID, TenantID: scope.TenantID, Level: scheduling.DepartmentQuota, Limits: scheduling.Quota{Guaranteed: limit, Burst: limit}},
		{ID: scope.ID, ParentID: "department", TenantID: scope.TenantID, Level: scheduling.WorkspaceQuota, Limits: scheduling.Quota{Guaranteed: limit, Burst: limit}},
	}
	if err := pgx.BeginFunc(t.Context(), accounts.DB, func(tx pgx.Tx) error {
		_, _, err := scheduling.ReplaceQuotaWithin(t.Context(), tx, 0, policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i, actor := range actors {
		out := read(actor, paths[i])
		body, err := json.Marshal(out)
		if err != nil || out.State != scheduling.WorkspaceQuotaConfigured || out.PolicyRevision != 1 ||
			len(out.Resources) != 2 || !strings.Contains(string(body), `"hard_limit":"2000"`) ||
			strings.Contains(string(body), "tenant_id") || strings.Contains(string(body), "department") ||
			strings.Contains(string(body), "available") || strings.Contains(string(body), "admitted") {
			t.Fatalf("public payload is inaccurate or exposes another scope: %s, %v", body, err)
		}
	}
	if _, err := accounts.DB.Exec(t.Context(), "UPDATE scheduling_quota_policy SET allocation='[]'::jsonb"); err != nil {
		t.Fatal(err)
	}
	value, status, failure := h.Handle(httptest.NewRecorder(), httptest.NewRequest("GET", paths[0], nil), admin, "")
	if value != nil || status != 0 || failure == nil || failure.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatalf("invalid installed policy exposed a success: %+v, %d, %v", value, status, failure)
	}
	problem := httptest.NewRecorder()
	httpserver.Problem(problem, "quota-status-fixture", failure)
	if problem.Code != 503 || strings.Contains(problem.Body.String(), "allocation") ||
		!strings.Contains(problem.Body.String(), "STORAGE_UNAVAILABLE") {
		t.Fatalf("unsafe or nonretryable public failure: %d, %s", problem.Code, problem.Body.String())
	}
	if _, err := accounts.DB.Exec(t.Context(),
		"UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1", portal.SessionID); err != nil {
		t.Fatal(err)
	}
	out, status, err := h.Handle(httptest.NewRecorder(), httptest.NewRequest("GET", paths[1], nil), portal, "")
	if err == nil || err.Error() != "AUTH_SESSION_EXPIRED" || out != nil || status != 0 {
		t.Fatalf("revoked actor received a success envelope: %+v, %d, %v", out, status, err)
	}
}
