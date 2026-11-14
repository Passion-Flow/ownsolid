//go:build localization_database_integration

package localization_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	internetmail "net/mail"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestM16ActualPopulated15UpgradeUnusedRollbackAndAllLocaleDowngradeFences(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.NewAtVersion(t, 15)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000016_localization.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal("unused actual downgrade", e)
	}
	if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=16"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB); e == nil {
		t.Fatal("old schema accepted")
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal("repeat real upgrade", e)
	}
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	invitation, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "locale-migration@example.invalid", "en", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	// Each real durable column independently prevents a lossy downgrade.
	for _, item := range []struct{ table, key, id string }{{"users", "id", a.Me.User.ID}, {"invitations", "id", invitation.ID}, {"mail_deliveries", "invitation_id", invitation.ID}} {
		if _, e = s.DB.Exec(ctx, "UPDATE "+item.table+" SET locale='fr' WHERE "+item.key+"=$1", item.id); e != nil {
			t.Fatal(e)
		}
		tx, e = s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(ctx, string(down))
		tx.Rollback(ctx)
		if e == nil || !strings.Contains(e.Error(), "LOCALIZATION_DOWNGRADE_IN_USE") {
			t.Fatal("used locale lost", item.table, e)
		}
		var saved string
		if e = s.DB.QueryRow(ctx, "SELECT locale FROM "+item.table+" WHERE "+item.key+"=$1", item.id).Scan(&saved); e != nil || saved != "fr" {
			t.Fatal("failed downgrade changed durable locale", e)
		}
		if _, e = s.DB.Exec(ctx, "UPDATE "+item.table+" SET locale='en' WHERE "+item.key+"=$1", item.id); e != nil {
			t.Fatal(e)
		}
	}
	for _, value := range []string{"en\nInjected", "not_a_locale", strings.Repeat("a", 36)} {
		if _, e = s.DB.Exec(ctx, "UPDATE users SET locale=$2 WHERE id=$1", a.Me.User.ID, value); e == nil {
			t.Fatal("invalid durable locale accepted")
		}
	}
}

func TestM16ActualCurrentProfileUnavailableRecoveryConcurrencyAuditAndRevokedSession(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	changed, e := s.UpdateMe(ctx, a, "Local profile \u4e2d\u6587", "zh-CN", a.Me.User.Version, uuid.NewString())
	if e != nil || changed.User.Locale != "zh-CN" {
		t.Fatal("actual installed locale", e)
	}
	if _, e = s.UpdateMe(ctx, a, "Stale", "en", a.Me.User.Version, uuid.NewString()); e == nil || e.Error() != "VERSION_CONFLICT" {
		t.Fatal("stale version changed language", e)
	}
	if _, e = s.UpdateMe(ctx, a, "Unsupported", "de", changed.User.Version, uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal("uninstalled new language accepted", e)
	}
	// Simulate restoring a real durable preference whose package is absent now.
	if _, e = s.DB.Exec(ctx, "UPDATE users SET locale='fr',version=version+1 WHERE id=$1", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	a = fixture.Login(t, s, "admin@example.invalid", password, "portal")
	preserved, e := s.UpdateMe(ctx, a, "Preserve stored missing language", "fr", a.Me.User.Version, uuid.NewString())
	if e != nil || preserved.User.Locale != "fr" {
		t.Fatal("missing pack silently rewrote preference", e)
	}
	if _, e = s.DB.Exec(ctx, `CREATE FUNCTION reject_locale_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='account.updated' THEN RAISE EXCEPTION 'EXPLICIT_AUDIT_FAILURE'; END IF; RETURN NEW; END; $$;CREATE TRIGGER reject_locale_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_locale_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateMe(ctx, a, "Must roll back", "en", preserved.User.Version, uuid.NewString()); e == nil {
		t.Fatal("audit failure committed profile")
	}
	actual, e := s.GetUser(ctx, a.Me.User.ID)
	if e != nil || actual.Version != preserved.User.Version || actual.Locale != "fr" || actual.DisplayName != preserved.User.DisplayName {
		t.Fatal("audit rollback lost profile integrity", e)
	}
	if _, e = s.DB.Exec(ctx, "DROP TRIGGER reject_locale_audit ON audit_events;DROP FUNCTION reject_locale_audit()"); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, locale := range []string{"en", "zh-CN"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			_, e := s.UpdateMe(ctx, a, "Concurrent language", ""+value, actual.Version, uuid.NewString())
			outcomes <- e
		}(locale)
	}
	wg.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for e := range outcomes {
		if e == nil {
			success++
		} else if e.Error() == "VERSION_CONFLICT" {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS did not serialize locale", success, conflict)
	}
	if _, e = s.DB.Exec(ctx, "UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='synthetic_locale_revoke' WHERE user_id=$1 AND revoked_at IS NULL", a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	actual, e = s.GetUser(ctx, a.Me.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateMe(ctx, a, "Revoked actor", "en", actual.Version, uuid.NewString()); e == nil {
		t.Fatal("revoked session changed locale")
	}
}

func TestM16ActualTLSAnonymousOfflineCatalogContractsAndCurrentProfileCSRF(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	directory := os.Getenv("PROJECT_ONE_LOCALE_PACKAGE_DIRECTORY")
	if directory == "" {
		t.Fatal("explicit actual font package required")
	}
	policy := config.DefaultLocalizationPolicy()
	policy.CacheSeconds = 0
	policy.MaximumLocales = 4
	registry, e := localization.New(config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: []string{"en", "zh-CN"}}, policy)
	if e != nil {
		t.Fatal(e)
	}
	s.Locales = registry
	h := identityhttp.New(s)
	server := httpfixture.NewTLSServer(t, h)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/localization/openapi.json")
	for k, v := range httpfixture.Schemas(t, "../../../../../api/openapi/v1/identity/openapi.json") {
		schemas[k] = v
	}
	client := httpfixture.New(t, server, "portal", schemas)
	catalog := client.Call("GET", "/api/v1/localization/catalog", nil, 200, "LocalizationCatalogResponse")
	if len(catalog["locales"].([]any)) != 2 {
		t.Fatal("anonymous inventory incomplete")
	}
	client.Call("GET", "/api/v1/localization/catalog?workspace_id="+uuid.NewString(), nil, 404, "Problem")
	client.Write("POST", "/api/v1/localization/catalog", map[string]any{}, 404, "Problem")
	client.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "")
	me := client.Call("GET", "/api/v1/me", nil, 200, "MeResponse")
	user := me["user"].(map[string]any)
	body := map[string]any{"display_name": "TLS locale profile", "locale": "zh-CN", "version": user["version"]}
	client.CSRF = ""
	client.Call("PATCH", "/api/v1/me", body, 403, "Problem")
	client.CSRF = client.Call("GET", "/api/v1/auth/csrf", nil, 200, "")["token"].(string)
	updated := client.Write("PATCH", "/api/v1/me", body, 200, "MeResponse")
	if updated["user"].(map[string]any)["locale"] != "zh-CN" {
		t.Fatal("actual profile API did not persist locale")
	}
	fresh := httpfixture.New(t, server, "admin", schemas)
	fresh.Write("POST", "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "")
	if fresh.Call("GET", "/api/v1/me", nil, 200, "MeResponse")["user"].(map[string]any)["locale"] != "zh-CN" {
		t.Fatal("new admin session lost locale")
	}
	raw, e := json.Marshal(catalog)
	if e != nil || bytes.Contains(raw, []byte(directory)) {
		t.Fatal("public catalog exposed operator path")
	}
}

func TestM16ActualInvitationAndOfflineSeedMailThroughIsolatedTLSReceiver(t *testing.T) {
	fixture.PreflightOwnedPostgres(t)
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	ctx := context.Background()
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	sink := smtpsink.Start(t, true, false)
	host, port, _ := net.SplitHostPort(sink.Listener.Addr().String())
	s.Mail.Config.SMTP.Host = host
	s.Mail.Config.SMTP.Port, _ = strconv.Atoi(port)
	s.Mail.Config.SMTP.CAFile = sink.CAFile
	s.Mail.Config.SMTP.TLSMode = "implicit_tls"
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 5
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 10
	if _, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "unsupported@example.invalid", "fr", 0, uuid.NewString()); e == nil || e.Error() != "VALIDATION_FAILED" {
		t.Fatal("new missing language invitation accepted", e)
	}
	invitation, e := s.Invite(ctx, a, "", "createInvitation", uuid.NewString(), "locale-synthetic@example.invalid", "zh-CN", 0, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	token := fixture.InvitationToken(t, s, invitation.ID)
	active, e := s.Mail.DispatchOne(ctx)
	if e != nil || !active {
		t.Fatal("actual isolated TLS delivery", e)
	}
	select {
	case raw := <-sink.Messages:
		message, e := internetmail.ReadMessage(bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(message.Body)
		if e != nil {
			t.Fatal(e)
		}
		if message.Header.Get("Content-Language") != "zh-CN" || !strings.HasPrefix(message.Header.Get("Content-Type"), "multipart/alternative") || len(body) == 0 {
			t.Fatal("actual delivered MIME language invalid")
		}
	case <-time.After(time.Second):
		t.Fatal("isolated receiver did not receive")
	}
	var state, locale string
	var payload []byte
	if e = s.DB.QueryRow(ctx, "SELECT state,locale,payload_ciphertext FROM mail_deliveries WHERE invitation_id=$1", invitation.ID).Scan(&state, &locale, &payload); e != nil || state != "sent" || locale != "zh-CN" || payload != nil {
		t.Fatal("delivery state/secrets/locale incorrect", e)
	}
	if _, e = s.ValidateInvitation(ctx, token); e != nil {
		t.Fatal("real delivered invitation unusable", e)
	}
}
