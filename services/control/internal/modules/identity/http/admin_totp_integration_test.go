package http_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	_ "embed"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

//go:embed testdata/admin_totp_candidate.sql
var adminTOTPFixtureSQL string

func candidateOTP(seed []byte, step int64) string {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], uint64(step))
	mac := hmac.New(sha1.New, seed)
	mac.Write(data[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[offset:offset+4])&0x7fffffff)%1_000_000)
}

func adminTOTPCandidateFixture(t *testing.T) (*identity.Service, *httptest.Server, *client, *client, string) {
	t.Helper()
	s, server, portal, admin, password := setup(t)
	if _, err := s.DB.Exec(t.Context(), adminTOTPFixtureSQL); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	keyring := filepath.Join(t.TempDir(), "totp-keyring.json")
	keyFile, err := json.Marshal(map[string]any{"active": "candidate", "keys": map[string]string{"candidate": base64.StdEncoding.EncodeToString(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyring, keyFile, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyring, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadAdminTOTPCandidateKeys(keyring); err == nil {
		t.Fatal("accepted a key file readable by others")
	}
	if err := os.Chmod(keyring, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadAdminTOTPCandidateKeys(keyring); err != nil {
		t.Fatal(err)
	}
	return s, server, portal, admin, password
}

func adminTOTPCandidateStep(t *testing.T, s *identity.Service) int64 {
	t.Helper()
	var now time.Time
	if err := s.DB.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	// Leave the HTTP request enough time to land in the chosen window.
	if remaining := 30 - now.Unix()%30; remaining < 3 {
		if _, err := s.DB.Exec(t.Context(), "SELECT pg_sleep_until($1)", time.Unix((now.Unix()/30+1)*30, 0)); err != nil {
			t.Fatal(err)
		}
		return now.Unix()/30 + 1
	}
	return now.Unix() / 30
}

func TestAdminTOTPCandidateUnmountedHTTPS(t *testing.T) {
	_, _, portal, admin, password := setup(t)
	credentials := map[string]string{"email": "admin@example.invalid", "password": password}
	admin.mutate(http.MethodPost, "/api/v1/auth/login", credentials, 200, "LoginResponse")
	problemCode(t, admin.mutate(http.MethodPost, "/api/v1/auth/login/totp", map[string]string{
		"challenge": security.Token(), "code": "000000",
	}, 404, "Problem"), "RESOURCE_NOT_FOUND")
	problemCode(t, admin.mutate(http.MethodPost, "/api/v1/me/totp/enrollment", map[string]string{"password": password}, 404, "Problem"), "RESOURCE_NOT_FOUND")
	change := map[string]string{"current_password": password, "new_password": fixture.Password(), "totp_code": "000000"}
	problemCode(t, admin.mutate(http.MethodPost, passwordChanges, change, 400, "Problem"), "VALIDATION_FAILED")
	portal.mutate(http.MethodPost, "/api/v1/auth/login", credentials, 200, "LoginResponse")
	problemCode(t, portal.mutate(http.MethodPost, passwordChanges, change, 400, "Problem"), "VALIDATION_FAILED")
}

func TestAdminTOTPCandidateHTTPS(t *testing.T) {
	s, server, portal, admin, password := adminTOTPCandidateFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const (
		email       = "admin@example.invalid"
		login       = "/api/v1/auth/login"
		totp        = "/api/v1/auth/login/totp"
		enrollment  = "/api/v1/me/totp/enrollment"
		confirm     = "/api/v1/me/totp/enrollment/confirmation"
		revoke      = "/api/v1/me/totp/revocation"
		me          = "/api/v1/me"
		adminCookie = "__Host-admin_session"
	)
	loginBody := map[string]any{"email": email, "password": password}
	adminPeer := newClient(t, server, "admin", admin.schemas)
	for _, browser := range []*client{portal, admin, adminPeer} {
		browser.mutate(http.MethodPost, login, loginBody, 200, "LoginResponse")
	}
	adminMe := admin.request(http.MethodGet, me, nil, 200, "MeResponse", nil)
	userID := adminMe["user"].(map[string]any)["id"].(string)
	problemCode(t, portal.mutate(http.MethodPost, enrollment, map[string]string{"password": password}, 404, "Problem"), "RESOURCE_NOT_FOUND")
	problemCode(t, admin.mutate(http.MethodPost, enrollment+"?tenant_id="+uuid.NewString(), map[string]string{"password": password}, 400, "Problem"), "VALIDATION_FAILED")
	problemCode(t, admin.mutate(http.MethodPost, enrollment, map[string]string{"password": "Synthetic incorrect password"}, 403, "Problem"), "PASSWORD_CURRENT_INVALID")
	setup := admin.mutate(http.MethodPost, enrollment, map[string]string{"password": password}, 201, "")
	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(setup["secret"].(string))
	if err != nil || len(seed) != 20 {
		t.Fatal("invalid enrollment seed")
	}
	var encrypted []byte
	if err := s.DB.QueryRow(ctx, "SELECT seed FROM admin_totp_factors WHERE user_id=$1", userID).Scan(&encrypted); err != nil ||
		bytes.Equal(encrypted, seed) || len(encrypted) != 36 {
		t.Fatal("factor seed was not encrypted", err)
	}
	var tenant, workspace string
	if err := s.DB.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace); err != nil {
		t.Fatal(err)
	}
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", foreignTenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign workspace','active')", foreignWorkspace, foreignTenant)
	selectInstallation := func(tenant, workspace string) {
		t.Helper()
		exec("UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1", tenant, workspace)
	}
	exec("UPDATE admin_totp_factors SET tenant_id=$1 WHERE user_id=$2", foreignTenant, userID)
	problemCode(t, admin.mutate(http.MethodPost, enrollment, map[string]string{"password": password}, 403, "Problem"), "ACCESS_DENIED")
	step := func() int64 { return adminTOTPCandidateStep(t, s) }
	confirmation := map[string]string{"id": setup["id"].(string), "code": candidateOTP(seed, step()-1)}
	problemCode(t, admin.mutate(http.MethodPost, confirm, confirmation, 400, "Problem"), "TOKEN_UNUSABLE")
	exec("UPDATE admin_totp_factors SET tenant_id=$1 WHERE user_id=$2", tenant, userID)
	problemCode(t, adminPeer.mutate(http.MethodPost, confirm, confirmation, 400, "Problem"), "TOKEN_UNUSABLE")
	problemCode(t, admin.mutate(http.MethodPost, confirm, map[string]string{"id": setup["id"].(string), "code": "abc123"}, 400, "Problem"), "VALIDATION_FAILED")
	admin.mutate(http.MethodPost, confirm, confirmation, 200, "")
	if len(admin.cookies) != 0 {
		t.Fatal("enrollment left pre-MFA Admin cookies")
	}
	problemCode(t, adminPeer.request(http.MethodGet, me, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	portal.request(http.MethodGet, me, nil, 200, "MeResponse", nil)

	// The common session issuer fences callers other than the TOTP completion.
	store, err := s.Repository()
	if err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err := s.DB.QueryRow(ctx, "SELECT auth_epoch FROM users WHERE id=$1", userID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		_, err := s.IssueSessionWithin(ctx, tx, userID, epoch, "admin", "", uuid.NewString(), nil)
		return err
	})
	if err == nil || err.Error() != "ACCESS_DENIED" {
		t.Fatalf("common issuer bypassed TOTP: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, s.DB.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	withoutKeys := &identity.Service{DB: pool, Config: s.Config, Passwords: s.Passwords, HMACKey: s.HMACKey, Mail: s.Mail}
	keyless := newClient(t, accountTLSServer(t, transport.New(withoutKeys)), "admin", admin.schemas)
	problemCode(t, keyless.mutate(http.MethodPost, login, loginBody, 503, "Problem"), "STORAGE_UNAVAILABLE")
	if keyless.cookies[adminCookie] != nil {
		t.Fatal("missing key file fell back to a password-only session")
	}

	start := func(c *client) string {
		t.Helper()
		page := c.mutate(http.MethodPost, login, loginBody, 202, "")
		challenge, ok := page["challenge"].(string)
		if !ok || !security.ValidToken(challenge) || len(page) != 2 || c.cookies[adminCookie] != nil {
			t.Fatal("password returned authority instead of a pending challenge")
		}
		return challenge
	}
	requestBody := func(challenge, code string) map[string]string {
		return map[string]string{"challenge": challenge, "code": code}
	}
	pending := start(admin)
	admin.request(http.MethodGet, me, nil, 401, "Problem", nil)
	input := requestBody(pending, candidateOTP(seed, step()))
	admin.refresh()
	foreignInput := maps.Clone(input)
	foreignInput["tenant_id"] = foreignTenant
	problemCode(t, admin.request(http.MethodPost, totp, foreignInput, 400, "Problem", nil), "VALIDATION_FAILED")
	selectInstallation(foreignTenant, foreignWorkspace)
	problemCode(t, admin.mutate(http.MethodPost, login, loginBody, 403, "Problem"), "ACCESS_DENIED")
	problemCode(t, admin.mutate(http.MethodPost, totp, input, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	selectInstallation(tenant, workspace)
	problemCode(t, admin.request(http.MethodPost, totp, input, 403, "Problem", map[string]string{"X-CSRF-Token": ""}), "CSRF_INVALID")
	problemCode(t, admin.request(http.MethodPost, totp, input, 403, "Problem", map[string]string{"Origin": ""}), "ORIGIN_REJECTED")
	other := newClient(t, server, "admin", admin.schemas)
	problemCode(t, other.mutate(http.MethodPost, totp, input, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	problemCode(t, portal.mutate(http.MethodPost, totp, input, 404, "Problem"), "RESOURCE_NOT_FOUND")
	exec("UPDATE admin_totp_challenges SET expires_at=clock_timestamp()-interval '1 second' WHERE challenge_hash=$1", security.Digest(pending))
	problemCode(t, admin.mutate(http.MethodPost, totp, input, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")

	pending = start(admin)
	problemCode(t, admin.mutate(http.MethodPost, totp, requestBody(pending, candidateOTP(seed, step()-4)), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	var sessions int
	if err := s.DB.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id=$1 AND audience='admin' AND revoked_at IS NULL", userID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("failed OTP issued a durable session", err)
	}
	// Two independent serving replicas race the same browser-bound request.
	replica := *s
	replica.DB, replica.Sources = pool, nil
	replicaBrowser := newClient(t, accountTLSServer(t, transport.New(&replica)), "admin", admin.schemas)
	replicaBrowser.cookies, replicaBrowser.csrf = maps.Clone(admin.cookies), admin.csrf
	acceptedStep := step()
	adminTOTPReplayRace(t, ctx, admin, replicaBrowser, totp, requestBody(pending, candidateOTP(seed, acceptedStep)))
	admin.request(http.MethodGet, me, nil, 200, "MeResponse", nil)
	otherPending := start(other)
	problemCode(t, other.mutate(http.MethodPost, totp, requestBody(otherPending, candidateOTP(seed, acceptedStep)), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	var attempts int
	rateKey := security.MAC(s.HMACKey, "m01-rate", "totp.verify", userID)
	if err := s.DB.QueryRow(ctx, "SELECT attempt_count FROM rate_limit_buckets WHERE bucket_key=$1", rateKey).Scan(&attempts); err != nil || attempts != 4 {
		t.Fatalf("expected committed enrollment, failure, login and replay attempts, got %d: %v", attempts, err)
	}

	selectInstallation(foreignTenant, foreignWorkspace)
	problemCode(t, admin.mutate(http.MethodPost, revoke, map[string]string{"password": password, "code": candidateOTP(seed, step()+1)}, 400, "Problem"), "TOKEN_UNUSABLE")
	selectInstallation(tenant, workspace)
	problemCode(t, admin.mutate(http.MethodPost, revoke, map[string]string{"password": password, "code": candidateOTP(seed, acceptedStep)}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	admin.mutate(http.MethodPost, revoke, map[string]string{"password": password, "code": candidateOTP(seed, step()+1)}, 204, "")
	if len(admin.cookies) != 0 {
		t.Fatal("revocation left Admin cookies")
	}
	problemCode(t, other.mutate(http.MethodPost, totp, requestBody(otherPending, candidateOTP(seed, step()+1)), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	portal.request(http.MethodGet, me, nil, 200, "MeResponse", nil)
	admin.mutate(http.MethodPost, login, loginBody, 200, "LoginResponse")
	var enabled, revoked int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='auth.totp_enabled'),count(*) FILTER (WHERE action='auth.totp_revoked')
		FROM audit_events WHERE actor_user_id=$1 AND subject_user_id=$1 AND audience='admin' AND metadata='{}'::jsonb`, userID).Scan(&enabled, &revoked); err != nil || enabled != 1 || revoked != 1 {
		t.Fatalf("expected one safe audit for each factor transition: %v", err)
	}
}

func adminTOTPReplayRace(t *testing.T, ctx context.Context, a, b *client, path string, body map[string]string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		status  int
		cookies []*http.Cookie
		body    []byte
		err     error
	}
	results := make(chan outcome, 2)
	for _, c := range []*client{a, b} {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.s.URL+cellroute.Public(path, wire.LocalScope()), bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "admin.example.invalid"
		request.Header.Set("Origin", "https://"+request.Host)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-CSRF-Token", c.csrf)
		for _, cookie := range c.cookies {
			request.AddCookie(cookie)
		}
		go func() {
			response, err := c.http.Do(request)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			results <- outcome{response.StatusCode, response.Cookies(), data, err}
		}()
	}
	accepted, refused := 0, 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		var document any
		if err := json.Unmarshal(result.body, &document); err != nil {
			t.Error(err)
			continue
		}
		switch result.status {
		case 200:
			accepted++
			if len(result.cookies) != 1 || result.cookies[0].Name != "__Host-admin_session" || a.schemas["LoginResponse"].Validate(document) != nil {
				t.Error("winning response did not issue the existing session contract")
			} else {
				a.cookies[result.cookies[0].Name] = result.cookies[0]
			}
		case 401:
			refused++
			if len(result.cookies) != 0 || document.(map[string]any)["code"] != "AUTH_INVALID_CREDENTIALS" {
				t.Error("replay returned unexpected authority or error")
			}
		default:
			t.Errorf("unexpected HTTP status in challenge race: %d", result.status)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("expected one challenge consumer, got %d accepted and %d refused", accepted, refused)
	}
}

func enrollAdminTOTPCandidate(t *testing.T, s *identity.Service, admin *client, password string) ([]byte, []any, string) {
	t.Helper()
	admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	factor := admin.mutate(http.MethodPost, "/api/v1/me/totp/enrollment", map[string]string{"password": password}, 201, "")
	seed, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(factor["secret"].(string))
	if err != nil {
		t.Fatal(err)
	}
	id := factor["id"].(string)
	payload := admin.mutate(http.MethodPost, "/api/v1/me/totp/enrollment/confirmation",
		map[string]string{"id": id, "code": candidateOTP(seed, adminTOTPCandidateStep(t, s)-1)}, 200, "")
	codes, ok := payload["recovery_codes"].([]any)
	if !ok || len(codes) != 10 {
		t.Fatal("enrollment did not return the one-time recovery values")
	}
	return seed, codes, id
}

func TestAdminTOTPCandidateRecoveryHTTPS(t *testing.T) {
	s, server, _, admin, password := adminTOTPCandidateFixture(t)
	_, codes, factor := enrollAdminTOTPCandidate(t, s, admin, password)
	ctx := t.Context()
	seen := map[string]bool{}
	for _, item := range codes {
		value := item.(string)
		if seen[value] || !security.ValidToken(value) {
			t.Fatal("recovery values must be independently generated")
		}
		seen[value] = true
	}
	var user, tenant string
	if err := s.DB.QueryRow(ctx, "SELECT user_id::text,tenant_id::text FROM admin_totp_factors WHERE id=$1", factor).Scan(&user, &tenant); err != nil {
		t.Fatal(err)
	}
	digest := func(value string) []byte {
		return security.MAC(s.HMACKey, "admin-totp-recovery", tenant, user, factor, value)
	}
	valid, expired := codes[0].(string), codes[1].(string)
	if _, err := s.DB.Exec(ctx, "UPDATE admin_totp_recovery_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE digest=$1", digest(expired)); err != nil {
		t.Fatal(err)
	}
	start := func(c *client) string {
		t.Helper()
		page := c.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 202, "")
		return page["challenge"].(string)
	}
	const complete = "/api/v1/auth/login/totp"
	input := func(challenge, value string) map[string]string {
		return map[string]string{"challenge": challenge, "code": value}
	}
	problemCode(t, admin.mutate(http.MethodPost, complete, input(security.Token(), valid), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	challenge := start(admin)
	problemCode(t, admin.mutate(http.MethodPost, complete, input(challenge, expired), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")

	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT fixture_refuses_mfa_login CHECK(action<>'auth.login_succeeded') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	admin.mutate(http.MethodPost, complete, input(challenge, valid), 503, "Problem")
	var remains bool
	if err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admin_totp_recovery_codes WHERE digest=$1)", digest(valid)).Scan(&remains); err != nil || !remains {
		t.Fatal("failed session audit consumed the recovery code", err)
	}
	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT fixture_refuses_mfa_login"); err != nil {
		t.Fatal(err)
	}
	admin.mutate(http.MethodPost, complete, input(challenge, valid), 200, "LoginResponse")
	other := newClient(t, server, "admin", admin.schemas)
	problemCode(t, other.mutate(http.MethodPost, complete, input(start(other), valid), 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	if err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM admin_totp_recovery_codes WHERE digest=$1)", digest(valid)).Scan(&remains); err != nil || remains {
		t.Fatal("successful session kept a replayable recovery code", err)
	}
}

func TestAdminTOTPCandidatePasswordStepUpHTTPS(t *testing.T) {
	s, server, _, admin, password := adminTOTPCandidateFixture(t)
	seed, codes, _ := enrollAdminTOTPCandidate(t, s, admin, password)
	const login, complete, change, me = "/api/v1/auth/login", "/api/v1/auth/login/totp", "/api/v1/me/password-changes", "/api/v1/me"
	email := "admin@example.invalid"
	challenge := admin.mutate(http.MethodPost, login, map[string]string{"email": email, "password": password}, 202, "")["challenge"].(string)
	signedInStep := adminTOTPCandidateStep(t, s)
	admin.mutate(http.MethodPost, complete, map[string]string{"challenge": challenge, "code": candidateOTP(seed, signedInStep)}, 200, "LoginResponse")
	stale := newClient(t, server, "admin", admin.schemas)
	stale.cookies = maps.Clone(admin.cookies)
	waiting := newClient(t, server, "admin", admin.schemas)
	staleChallenge := waiting.mutate(http.MethodPost, login, map[string]string{"email": email, "password": password}, 202, "")["challenge"].(string)
	changed := "Synthetic replacement " + uuid.NewString()
	body := map[string]string{"current_password": password, "new_password": changed}
	problemCode(t, admin.mutate(http.MethodPost, change, body, 403, "Problem"), "ACCESS_DENIED")
	body["totp_code"] = codes[0].(string)
	problemCode(t, admin.mutate(http.MethodPost, change, body, 400, "Problem"), "VALIDATION_FAILED")
	body["totp_code"] = candidateOTP(seed, signedInStep)
	problemCode(t, admin.mutate(http.MethodPost, change, body, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	stale.request(http.MethodGet, me, nil, 200, "MeResponse", nil)

	accepted := candidateOTP(seed, adminTOTPCandidateStep(t, s)+1)
	body["totp_code"] = accepted
	foreignBody := maps.Clone(body)
	foreignBody["tenant_id"] = uuid.NewString()
	problemCode(t, admin.mutate(http.MethodPost, change, foreignBody, 400, "Problem"), "VALIDATION_FAILED")
	ctx := t.Context()
	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events ADD CONSTRAINT fixture_refuses_mfa_change CHECK(action<>'auth.password_changed') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	problemCode(t, admin.mutate(http.MethodPost, change, body, 503, "Problem"), "STORAGE_UNAVAILABLE")
	stale.request(http.MethodGet, me, nil, 200, "MeResponse", nil)
	if _, err := s.DB.Exec(ctx, "ALTER TABLE audit_events DROP CONSTRAINT fixture_refuses_mfa_change"); err != nil {
		t.Fatal(err)
	}
	admin.mutate(http.MethodPost, change, body, 200, "MeResponse")
	problemCode(t, stale.request(http.MethodGet, me, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	problemCode(t, waiting.mutate(http.MethodPost, complete, map[string]string{"challenge": staleChallenge, "code": codes[0].(string)}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	other := newClient(t, server, "admin", admin.schemas)
	problemCode(t, other.mutate(http.MethodPost, login, map[string]string{"email": email, "password": password}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	challenge = other.mutate(http.MethodPost, login, map[string]string{"email": email, "password": changed}, 202, "")["challenge"].(string)
	problemCode(t, other.mutate(http.MethodPost, complete, map[string]string{"challenge": challenge, "code": accepted}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	var count int
	if err := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action='auth.password_changed' AND audience='admin'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expected one authorized credential replacement, got %d: %v", count, err)
	}
}

func TestAdminTOTPCandidatePasswordResetKeepsFactorHTTPS(t *testing.T) {
	s, _, portal, admin, password := adminTOTPCandidateFixture(t)
	seed, _, _ := enrollAdminTOTPCandidate(t, s, admin, password)
	const email, login = "admin@example.invalid", "/api/v1/auth/login"
	pending := admin.mutate(http.MethodPost, login, map[string]string{"email": email, "password": password}, 202, "")["challenge"].(string)
	portal.mutate(http.MethodPost, "/api/v1/auth/password-reset-requests", map[string]string{"email": email}, 202, "AcceptedResponse")
	replacement := fixture.Password()
	portal.mutate(http.MethodPost, "/api/v1/auth/password-resets", map[string]string{
		"token": fixture.ResetToken(t, s, email), "password": replacement,
	}, 200, "ResetResponse")

	otp := candidateOTP(seed, adminTOTPCandidateStep(t, s))
	const complete = "/api/v1/auth/login/totp"
	problemCode(t, admin.mutate(http.MethodPost, complete, map[string]string{"challenge": pending, "code": otp}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	problemCode(t, admin.mutate(http.MethodPost, login, map[string]string{"email": email, "password": password}, 401, "Problem"), "AUTH_INVALID_CREDENTIALS")
	pending = admin.mutate(http.MethodPost, login, map[string]string{"email": email, "password": replacement}, 202, "")["challenge"].(string)
	if admin.cookies["__Host-admin_session"] != nil {
		t.Fatal("password reset bypassed the enrolled factor")
	}
	admin.mutate(http.MethodPost, complete, map[string]string{"challenge": pending, "code": otp}, 200, "LoginResponse")
}
