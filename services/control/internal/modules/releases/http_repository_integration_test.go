//go:build repository_integration

package releases_test

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases"
	releasehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

// Only the immutable snapshot reader is a scripted double. TLS, both original
// HTTP entrances/login/session checks, selected PostgreSQL transactions and audit
// are real. This is not openReleaseView/fullserve or cross-UID file qualification.
func TestActualReleaseHTTPAuthorityAuditAndFreshness(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Fatal("RELEASE_HTTP_EXPLICIT_ISOLATED_DATABASE_REQUIRED")
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	auth := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	handler := identityhttp.New(accounts)
	a := releaseHTTPSnapshot(3, "a")
	b := releaseHTTPSnapshot(4, "b")
	for _, snapshot := range []view.Snapshot{a, b} {
		if err := snapshot.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	c := releases.Config{Directory: t.TempDir(), ScopeID: a.ScopeID, ManifestSHA256: a.ManifestSHA256,
		ConfigurationSHA256: a.ConfigurationSHA256, PublisherUID: os.Geteuid(), PageSize: 1, OperationSeconds: 10}
	// The ordinary constructor still refuses same-UID publication. The explicit
	// test factory is the sole replacement and is not in the production binary.
	actual, err := releases.New(accounts, handler.Locales, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = actual.VerifyStored(t.Context()); err == nil || err.Error() != "RELEASE_VIEW_UNAVAILABLE" {
		t.Fatal("production same-UID reader unexpectedly admitted", err)
	}
	if _, err = releases.NewSnapshotReaderTestService(accounts, handler.Locales, c, nil); err == nil {
		t.Fatal("nil test reader admitted")
	}
	reader := &releaseHTTPScript{current: a}
	service, err := releases.NewSnapshotReaderTestService(accounts, handler.Locales, c, reader.read)
	if err != nil {
		t.Fatal(err)
	}
	handler.Releases = &releasehttp.Handler{Service: service}
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/releases/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	anonymous := httpfixture.New(t, server, "admin", schemas)
	count := func() int {
		t.Helper()
		var n int
		if err := accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action IN ('release.current.read','release.history.read')").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	problem := func(client *httpfixture.Client, path string, status int, code string) {
		t.Helper()
		before := count()
		out := client.Call("GET", path, nil, status, "")
		if len(out) != 5 || out["type"] != "about:blank" || out["code"] != code || out["title"] != code || out["status"] != float64(status) {
			t.Fatal("unexpected or data-bearing release problem", out)
		}
		id, ok := out["request_id"].(string)
		parsed, e := uuid.Parse(id)
		if !ok || e != nil || parsed == uuid.Nil || parsed.String() != id {
			t.Fatal("invalid problem request id")
		}
		var n int
		if e = accounts.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE request_id=$1", id).Scan(&n); e != nil || n != 0 || count() != before {
			t.Fatal("refused release request committed audit", n, e)
		}
	}
	expectedAudits := map[string]int{}
	audit := func(action string, snapshot view.Snapshot, expected int) {
		t.Helper()
		if count() != expected {
			t.Fatal("release audit count", count(), expected)
		}
		key := action + ":" + snapshot.HeadSHA256
		expectedAudits[key]++
		var id string
		var n int
		if err := accounts.DB.QueryRow(t.Context(), `SELECT count(*),COALESCE(MAX(request_id::text),'') FROM audit_events
   WHERE action=$1 AND actor_user_id=$2 AND subject_user_id IS NULL AND audience='admin' AND outcome='success'
   AND target_type='release_installation' AND target_id IS NULL AND reason=$3 AND tenant_id=$4 AND workspace_id=$5`,
			action, auth.Me.User.ID, "scope="+snapshot.ScopeID+" head="+snapshot.HeadSHA256, auth.Me.Workspace.TenantID, auth.Me.Workspace.ID).Scan(&n, &id); err != nil || n != expectedAudits[key] {
			t.Fatal("release read audit binding", n, err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			t.Fatal("audit request id invalid")
		}
	}
	current := func(expected view.Snapshot, auditCount int) {
		t.Helper()
		out := admin.Call("GET", releasehttp.CurrentPath, nil, 200, "")
		if err := schemas["Current"].Validate(out); err != nil {
			t.Fatal("current response schema", err)
		}
		var schema int
		if err := accounts.DB.QueryRow(t.Context(), "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		observed := out["snapshot"].(map[string]any)
		if out["database"] != "postgresql" || out["applied_schema"] != float64(schema) || schema < 1 || out["live_health"] != false || len(out["locales"].([]any)) != len(handler.Locales.Catalog()) || len(out["locales"].([]any)) == 0 || observed["head_sha256"] != expected.HeadSHA256 || observed["sequence"] != float64(expected.Sequence) {
			t.Fatal("current installation projection mismatch")
		}
		audit("release.current.read", expected, auditCount)
	}
	history := func(cursor string, sequence int, expected view.Snapshot, auditCount int) map[string]any {
		t.Helper()
		path := releasehttp.HistoryPath
		if cursor != "" {
			path += "?cursor=" + url.QueryEscape(cursor)
		}
		out := admin.Call("GET", path, nil, 200, "")
		if err := schemas["History"].Validate(out); err != nil {
			t.Fatal("history response schema", err)
		}
		items := out["items"].([]any)
		if out["scope_id"] != expected.ScopeID || out["head_sha256"] != expected.HeadSHA256 || out["sequence"] != float64(expected.Sequence) || out["first_sequence"] != float64(expected.FirstSequence) || len(items) != 1 || items[0].(map[string]any)["sequence"] != float64(sequence) {
			t.Fatal("history order or snapshot binding mismatch")
		}
		audit("release.history.read", expected, auditCount)
		return out
	}
	problem(anonymous, releasehttp.CurrentPath, 401, "AUTH_REQUIRED")
	if reader.calls() != 0 {
		t.Fatal("anonymous request reached snapshot reader")
	}
	for _, client := range []*httpfixture.Client{admin, portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	problem(portal, releasehttp.CurrentPath, 404, "RESOURCE_NOT_FOUND")
	if reader.calls() != 0 {
		t.Fatal("Portal request reached snapshot reader")
	}
	current(a, 1)
	first := history("", 3, a, 2)
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("history cursor missing")
	}
	second := history(cursor, 2, a, 3)
	next, ok := second["next_cursor"].(string)
	if !ok || next == "" {
		t.Fatal("second history cursor missing")
	}
	if last := history(next, 1, a, 4); last["next_cursor"] != nil {
		t.Fatal("history continuation beyond suffix")
	}
	before := reader.calls()
	for _, path := range []string{releasehttp.CurrentPath + "?scope=foreign", releasehttp.CurrentPath + "?path=/tmp/foreign", releasehttp.HistoryPath + "?limit=1", releasehttp.HistoryPath + "?cursor=a&cursor=b"} {
		problem(admin, path, 400, "VALIDATION_FAILED")
	}
	if reader.calls() != before {
		t.Fatal("invalid selector reached snapshot reader")
	}
	reader.set(b)
	before = reader.calls()
	problem(admin, releasehttp.HistoryPath+"?cursor="+url.QueryEscape(cursor), 409, "RELEASE_VIEW_CHANGED")
	if reader.calls() != before+1 {
		t.Fatal("stale cursor did not fail at snapshot binding")
	}
	// A changed second read must discard the already constructed success DTO and
	// roll back before the audit insert. An explicit later request may see B.
	reader.set(b, a, b)
	before = reader.calls()
	problem(admin, releasehttp.CurrentPath, 409, "RELEASE_VIEW_CHANGED")
	if reader.calls() != before+2 {
		t.Fatal("missing post-read freshness fence")
	}
	current(b, 5)
	if _, err := accounts.DB.Exec(t.Context(), `CREATE FUNCTION release_http_refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$
  BEGIN IF NEW.action IN ('release.current.read','release.history.read') THEN RAISE EXCEPTION 'SYNTHETIC_RELEASE_AUDIT_REFUSAL'; END IF; RETURN NEW; END; $$;
  CREATE TRIGGER release_http_refuse_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION release_http_refuse_audit()`); err != nil {
		t.Fatal(err)
	}
	before = reader.calls()
	problem(admin, releasehttp.CurrentPath, 503, "RELEASE_VIEW_UNAVAILABLE")
	problem(admin, releasehttp.HistoryPath, 503, "RELEASE_VIEW_UNAVAILABLE")
	if reader.calls() != before+4 {
		t.Fatal("audit failure did not follow real release reads")
	}
	if _, err := accounts.DB.Exec(t.Context(), "DROP TRIGGER release_http_refuse_audit ON audit_events; DROP FUNCTION release_http_refuse_audit()"); err != nil {
		t.Fatal(err)
	}
	current(b, 6)
	history("", 4, b, 7)
	// The real HTTP Authenticate query must reject a still-present session after
	// current SQL Admin authority is revoked, before the module reader is called.
	if tag, err := accounts.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=false WHERE id=$1", auth.Me.User.ID); err != nil || tag.RowsAffected() != 1 {
		t.Fatal("current Admin revocation", err)
	}
	before = reader.calls()
	problem(admin, releasehttp.CurrentPath, 401, "AUTH_SESSION_EXPIRED")
	problem(admin, releasehttp.HistoryPath, 401, "AUTH_SESSION_EXPIRED")
	if reader.calls() != before || count() != 7 {
		t.Fatal("revoked Admin reached release data or audit")
	}
	t.Log("RELEASE_HTTP_ACTUAL_SQL_AUTHORITY_AUDIT_FRESHNESS_COMPLETE snapshot_reader=explicit_test_double fullserve=false publisher_uid_qualification=false")
}

func releaseHTTPSnapshot(sequence int, head string) view.Snapshot {
	s := view.Snapshot{Format: view.Format, ScopeID: strings.Repeat("1", 32), ReleaseID: "synthetic-release", Profile: "test-fixture-linux-amd64",
		ArchiveSHA256: strings.Repeat("2", 64), ManifestSHA256: strings.Repeat("3", 64), ConfigurationSHA256: strings.Repeat("4", 64),
		Sequence: sequence, HeadSHA256: strings.Repeat(head, 64), JournalBytes: int64(sequence * 100), FirstSequence: 1}
	for i := 1; i <= sequence; i++ {
		s.Events = append(s.Events, view.Event{Sequence: i, OperationID: fmt.Sprintf("%032x", i), Action: "release_prepare", Outcome: "completed", Time: fmt.Sprintf("2026-01-01T00:00:%02dZ", i)})
	}
	s.RecordedAt = s.Events[len(s.Events)-1].Time
	return s
}

type releaseHTTPScript struct {
	mu       sync.Mutex
	current  view.Snapshot
	sequence []view.Snapshot
	count    int
}

func (r *releaseHTTPScript) read(c releases.Config) (view.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.count++
	s := r.current
	if len(r.sequence) > 0 {
		s = r.sequence[0]
		r.sequence = r.sequence[1:]
	}
	if s.Validate() != nil || s.ScopeID != c.ScopeID || s.ManifestSHA256 != c.ManifestSHA256 || s.ConfigurationSHA256 != c.ConfigurationSHA256 {
		return view.Snapshot{}, errors.New("RELEASE_VIEW_UNAVAILABLE")
	}
	s.Events = append([]view.Event(nil), s.Events...)
	return s, nil
}
func (r *releaseHTTPScript) set(current view.Snapshot, sequence ...view.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = current
	r.sequence = append([]view.Snapshot(nil), sequence...)
}
func (r *releaseHTTPScript) calls() int { r.mu.Lock(); defer r.mu.Unlock(); return r.count }
