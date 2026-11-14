//go:build repository_integration && fullserve_browser_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

type nativeRecoveryRequest func(string, string, string, any, string, []*http.Cookie, int) map[string]any

type nativeRateRestart struct {
	ctx      context.Context
	db       *pgxpool.Pool
	policy   config.RuntimePolicy
	key      []byte
	request  nativeRecoveryRequest
	csrf     map[string]string
	before   int
	after    int
	expired  bool
	cooldown bool
}

func (r *nativeRateRestart) post(t *testing.T, audience, path string, body map[string]string, want int) {
	t.Helper()
	result := r.request(audience, "POST", path, body, r.csrf[audience], nil, want)
	code := "AUTH_INVALID_CREDENTIALS"
	if want == 429 {
		code = "RATE_LIMITED"
	}
	if want == 202 {
		if result["accepted"] != true {
			t.Fatal("NATIVE_RATE_ACCEPTANCE_INVALID")
		}
	} else if result["code"] != code {
		t.Fatal("NATIVE_RATE_ERROR_CONTRACT_INVALID")
	}
}

func (r *nativeRateRestart) authorityCounts(t *testing.T) [2]int {
	t.Helper()
	var counts [2]int
	if r.db.QueryRow(r.ctx, "SELECT (SELECT count(*) FROM password_reset_tokens),(SELECT count(*) FROM mail_deliveries)").Scan(&counts[0], &counts[1]) != nil {
		t.Fatal("NATIVE_RATE_AUTHORITY_COUNTS_UNAVAILABLE")
	}
	return counts
}

func (r *nativeRateRestart) quiet(t *testing.T, audience string) {
	t.Helper()
	before := r.authorityCounts(t)
	r.post(t, audience, "/api/v1/auth/password-reset-requests", map[string]string{"email": "admin@example.invalid"}, 202)
	if r.authorityCounts(t) != before {
		t.Fatal("NATIVE_RATE_QUIET_REQUEST_CREATED_AUTHORITY")
	}
}

func (r *nativeRateRestart) moveCooldown(t *testing.T) {
	t.Helper()
	var now time.Time
	if r.db.QueryRow(r.ctx, "SELECT clock_timestamp()").Scan(&now) != nil {
		t.Fatal("NATIVE_RATE_DATABASE_CLOCK_UNAVAILABLE")
	}
	start := now.Add(-time.Duration(r.policy.Rates.ResetCooldownSeconds+1) * time.Second)
	expiry := start.Add(time.Duration(r.policy.Rates.ResetEmailWindowSeconds) * time.Second)
	key := security.MAC(r.key, "m01-rate", "reset.email", "admin@example.invalid")
	result, e := r.db.Exec(r.ctx, "UPDATE rate_limit_buckets SET window_started_at=$2,expires_at=$3,last_admitted_at=$2 WHERE bucket_key=$1", key, start, expiry)
	if e != nil || result.RowsAffected() != 1 {
		t.Fatal("NATIVE_RATE_EXACT_COOLDOWN_CLOCK_FIXTURE_FAILED")
	}
}

func (r *nativeRateRestart) limited(t *testing.T) int {
	t.Helper()
	for _, audience := range []string{"portal", "admin"} {
		r.post(t, audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": "synthetic rejected login"}, 429)
	}
	r.post(t, "portal", "/api/v1/auth/login", map[string]string{"email": "overflow-login@example.invalid", "password": "synthetic rejected login"}, 429)
	r.post(t, "portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "overflow-reset@example.invalid"}, 429)
	r.quiet(t, "admin")
	return 4
}

func nativePrepareRateRestart(t *testing.T, ctx context.Context, db *pgxpool.Pool, policy config.RuntimePolicy, key []byte, wrongPassword string, request nativeRecoveryRequest) *nativeRateRestart {
	t.Helper()
	p := policy.Rates
	if p.LoginEmailCount != 10 || p.LoginIPCount != 30 || p.ResetEmailCount != 3 || p.ResetIPCount != 20 || p.LoginEmailWindowSeconds != 900 || p.LoginIPWindowSeconds != 300 || p.ResetEmailWindowSeconds != 900 || p.ResetIPWindowSeconds != 900 || p.ResetCooldownSeconds != 60 {
		t.Fatal("NATIVE_RATE_CANONICAL_PROFILE_DRIFT")
	}
	// Explicit disposable fixture boundary; no concurrent request is active.
	if _, e := db.Exec(ctx, "DELETE FROM rate_limit_buckets"); e != nil {
		t.Fatal("NATIVE_RATE_SYNTHETIC_PHASE_RESET_FAILED")
	}
	r := &nativeRateRestart{ctx: ctx, db: db, policy: policy, key: key, request: request, csrf: map[string]string{}}
	for _, audience := range []string{"portal", "admin"} {
		result := request(audience, "GET", "/api/v1/auth/csrf", nil, "", nil, 200)
		token, ok := result["token"].(string)
		if !ok || token == "" {
			t.Fatal("NATIVE_RATE_CSRF_REQUIRED")
		}
		r.csrf[audience] = token
	}
	portalLogins := 0
	for i := 0; i < p.LoginEmailCount; i++ {
		audience := "admin"
		if i%2 == 0 {
			audience = "portal"
			portalLogins++
		}
		r.post(t, audience, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": wrongPassword}, 401)
	}
	for portalLogins < p.LoginIPCount {
		r.post(t, "portal", "/api/v1/auth/login", map[string]string{"email": "rate-login-" + strconv.Itoa(portalLogins) + "@example.invalid", "password": wrongPassword}, 401)
		portalLogins++
	}
	r.post(t, "portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "admin@example.invalid"}, 202)
	r.quiet(t, "portal")
	r.cooldown = true
	for i := 1; i < p.ResetEmailCount; i++ {
		r.moveCooldown(t)
		r.post(t, "portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "admin@example.invalid"}, 202)
	}
	r.moveCooldown(t)
	r.quiet(t, "portal")
	for i := p.ResetEmailCount + 2; i < p.ResetIPCount; i++ {
		r.post(t, "portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "rate-reset-" + strconv.Itoa(i) + "@example.invalid"}, 202)
	}
	for _, v := range []struct {
		purpose, identifier string
		count               int
	}{{"login.email", "admin@example.invalid", p.LoginEmailCount}, {"login.ip.portal", "127.0.0.1", p.LoginIPCount}, {"reset.email", "admin@example.invalid", p.ResetEmailCount}, {"reset.ip.portal", "127.0.0.1", p.ResetIPCount}} {
		var count int
		if db.QueryRow(ctx, "SELECT attempt_count FROM rate_limit_buckets WHERE bucket_key=$1", security.MAC(key, "m01-rate", v.purpose, v.identifier)).Scan(&count) != nil || count != v.count {
			t.Fatal("NATIVE_RATE_CANONICAL_BUCKET_COUNT_INVALID")
		}
	}
	r.before = r.limited(t)
	return r
}

func nativeRateFingerprint(t *testing.T, ctx context.Context, db *pgxpool.Pool) string {
	t.Helper()
	var raw string
	if db.QueryRow(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY bucket_key)::text,'[]') FROM rate_limit_buckets b").Scan(&raw) != nil || raw == "[]" {
		t.Fatal("NATIVE_RATE_PRIVATE_COUNTER_FINGERPRINT_FAILED")
	}
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (r *nativeRateRestart) expireAndRecover(t *testing.T) {
	t.Helper()
	if _, e := r.db.Exec(r.ctx, "UPDATE rate_limit_buckets SET window_started_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 day',last_admitted_at=clock_timestamp()-interval '2 days'"); e != nil {
		t.Fatal("NATIVE_RATE_OWNED_EXPIRY_CLOCK_FIXTURE_FAILED")
	}
	// Actual recovered reset consumption clears its identity context cookie.
	for _, audience := range []string{"portal", "admin"} {
		result := r.request(audience, "GET", "/api/v1/auth/csrf", nil, "", nil, 200)
		token, ok := result["token"].(string)
		if !ok || token == "" {
			t.Fatal("NATIVE_RATE_RECOVERY_CSRF_REQUIRED")
		}
		r.csrf[audience] = token
	}
	r.post(t, "portal", "/api/v1/auth/login", map[string]string{"email": "after-expiry@example.invalid", "password": "synthetic rejected login"}, 401)
	before := r.authorityCounts(t)
	r.post(t, "portal", "/api/v1/auth/password-reset-requests", map[string]string{"email": "admin@example.invalid"}, 202)
	after := r.authorityCounts(t)
	if after[0] != before[0]+1 || after[1] != before[1]+1 {
		t.Fatal("NATIVE_RATE_EXPIRY_DID_NOT_RESTORE_REAL_RECOVERY")
	}
	r.expired = true
}

func (r *nativeRateRestart) result(before, after string) map[string]any {
	return map[string]any{"profile": "canonical_default_limits_v1", "limits": map[string]int{"login_email": r.policy.Rates.LoginEmailCount, "login_ip": r.policy.Rates.LoginIPCount, "reset_email": r.policy.Rates.ResetEmailCount, "reset_ip": r.policy.Rates.ResetIPCount}, "counter_sha256_before": before, "counter_sha256_after": after, "denials_before": r.before, "denials_after": r.after, "quiet_email_before_after": r.before == 4 && r.after == 4, "cooldown_quiet_verified": r.cooldown, "expired_unknown_login_and_known_reset_admitted": r.expired}
}

type nativePendingMail struct {
	tokens     map[string]string
	resetToken string
}

func nativeCapturePendingMail(t *testing.T, ctx context.Context, db *pgxpool.Pool, keys *mail.Keyring, origin string) nativePendingMail {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var sending int
		if db.QueryRow(ctx, "SELECT count(*) FROM mail_deliveries WHERE state='sending'").Scan(&sending) != nil {
			t.Fatal("NATIVE_PENDING_MAIL_LEASE_READ_FAILED")
		}
		if sending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("NATIVE_PENDING_MAIL_MUST_BE_QUIESCENT")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, e := db.Query(ctx, "SELECT id::text,message_id,purpose,payload_key_id,payload_nonce,payload_ciphertext FROM mail_deliveries WHERE state='queued' ORDER BY id")
	if e != nil {
		t.Fatal("NATIVE_PENDING_MAIL_PRIVATE_READ_FAILED")
	}
	defer rows.Close()
	result := nativePendingMail{tokens: map[string]string{}}
	counts := map[string]int{}
	for rows.Next() {
		var id, message, purpose, key string
		var nonce, cipher []byte
		if rows.Scan(&id, &message, &purpose, &key, &nonce, &cipher) != nil || len(result.tokens) >= 16 || message == "" {
			t.Fatal("NATIVE_PENDING_MAIL_PRIVATE_ROW_INVALID")
		}
		raw, e := keys.Decrypt(key, id, purpose, nonce, cipher)
		var payload mail.Payload
		if e != nil || json.Unmarshal(raw, &payload) != nil {
			t.Fatal("NATIVE_PENDING_MAIL_PRIVATE_PAYLOAD_INVALID")
		}
		token := ""
		if purpose == "invitation" || purpose == "password_reset" {
			link, e := url.Parse(payload.Link)
			path := "/register"
			if purpose == "password_reset" {
				path = "/reset-password"
			}
			if e != nil || link.Scheme+"://"+link.Host != origin || link.Path != path || link.RawQuery != "" || link.User != nil || !strings.HasPrefix(link.Fragment, "token=") {
				t.Fatal("NATIVE_PENDING_MAIL_LINK_AUTHORITY_INVALID")
			}
			token = strings.TrimPrefix(link.Fragment, "token=")
			if !security.ValidToken(token) {
				t.Fatal("NATIVE_PENDING_MAIL_TOKEN_INVALID")
			}
			if purpose == "password_reset" {
				result.resetToken = token
			}
		} else if purpose != "password_changed" {
			t.Fatal("NATIVE_PENDING_MAIL_PURPOSE_INVALID")
		}
		message = "<" + message + ">"
		if _, exists := result.tokens[message]; exists {
			t.Fatal("NATIVE_PENDING_MAIL_MESSAGE_ID_DUPLICATED")
		}
		result.tokens[message] = token
		counts[purpose]++
	}
	if rows.Err() != nil || counts["invitation"] != 1 || counts["password_reset"] < 1 || counts["password_reset"] > config.DefaultRuntimePolicy().Identity.MaxLiveResetTokens {
		t.Fatalf("NATIVE_PENDING_BOTH_LINK_PURPOSES_REQUIRED invitations=%d resets=%d queued=%d", counts["invitation"], counts["password_reset"], len(result.tokens))
	}
	t.Logf("NATIVE_PENDING_MAIL_COUNTS invitations=%d resets=%d queued=%d private_tokens_not_retained=true", counts["invitation"], counts["password_reset"], len(result.tokens))
	return result
}

func nativeVerifyMailParts(t *testing.T, body io.Reader, contentType, token string, depth int, seen map[string]bool) int {
	t.Helper()
	media, parameters, e := mime.ParseMediaType(contentType)
	if e != nil || depth > 1 || (media != "multipart/alternative" && media != "multipart/related") || parameters["boundary"] == "" {
		t.Fatal("NATIVE_PENDING_MAIL_MULTIPART_INVALID")
	}
	reader, parts, texts := multipart.NewReader(body, parameters["boundary"]), 0, 0
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		parts++
		if e != nil || parts > 3 {
			t.Fatal("NATIVE_PENDING_MAIL_PART_BOUND_EXCEEDED")
		}
		kind, _, e := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if e != nil {
			t.Fatal("NATIVE_PENDING_MAIL_PART_INVALID")
		}
		if kind == "multipart/alternative" {
			texts += nativeVerifyMailParts(t, part, part.Header.Get("Content-Type"), token, depth+1, seen)
		} else {
			if part.Header.Get("Content-Transfer-Encoding") != "base64" || (kind != "text/plain" && kind != "text/html" && kind != "image/png") {
				t.Fatal("NATIVE_PENDING_MAIL_ENCODING_INVALID")
			}
			plain, e := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, part), 65537))
			if e != nil || len(plain) > 65536 {
				t.Fatal("NATIVE_PENDING_MAIL_DECODE_BOUND_EXCEEDED")
			}
			if kind != "image/png" {
				if seen[kind] || token != "" && !bytes.Contains(plain, []byte(token)) {
					t.Fatal("NATIVE_PENDING_MAIL_DECODED_LINK_CHANGED")
				}
				seen[kind] = true
				texts++
			}
		}
		part.Close()
	}
	return texts
}

func (p nativePendingMail) receive(t *testing.T, sink *smtpsink.Sink) {
	t.Helper()
	seen := map[string]bool{}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(seen) < len(p.tokens) {
		select {
		case raw := <-sink.Messages:
			message, e := stdmail.ReadMessage(bytes.NewReader(raw))
			if e != nil {
				t.Fatal("NATIVE_PENDING_MAIL_HEADER_INVALID")
			}
			id := message.Header.Get("Message-ID")
			token, exists := p.tokens[id]
			if !exists || seen[id] || nativeVerifyMailParts(t, message.Body, message.Header.Get("Content-Type"), token, 0, map[string]bool{}) != 2 {
				t.Fatal("NATIVE_PENDING_MAIL_RECEIPT_BINDING_INVALID")
			}
			seen[id] = true
		case <-timer.C:
			t.Fatal("NATIVE_PENDING_BOTH_LINK_DELIVERIES_TIMEOUT")
		}
	}
}
