package boundary

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	relyingparty "github.com/zitadel/oidc/v3/pkg/client/rp"
	"github.com/zitadel/oidc/v3/pkg/oidc"
)

func (f *protocolFixture) remote() relyingparty.RelyingParty {
	f.app.mu.Lock()
	defer f.app.mu.Unlock()
	return f.app.remote
}
func TestLiveOPRPNonceIssuerAndAudience(t *testing.T) {
	f := newProtocolFixture(t, nil)
	mismatch := f.issueCode(t, func(v url.Values) { v.Set("nonce", random(t)) })
	response := f.request(t, f.browser, "GET", mismatch.Callback, nil, "")
	var refusal map[string]json.RawMessage
	if json.Unmarshal(responseBody(t, response, 401), &refusal) != nil {
		t.Fatal("RP nonce refusal invalid")
	}
	noTokenFields(t, refusal)
	select {
	case <-f.app.results:
		t.Fatal("RP accepted unmatched nonce")
	default:
	}
	if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND consumed_at IS NOT NULL`, mismatch.RequestID) != 1 {
		t.Fatal("nonce refusal did not follow actual successful token exchange")
	}
	fresh := f.issueCode(t, nil)
	response = f.request(t, f.browser, "GET", fresh.Callback, nil, "")
	responseBody(t, response, 200)
	issued := f.rpResult(t)
	remote := f.remote()
	for _, name := range []string{"issuer", "audience", "nonce", "signature"} {
		t.Run(name, func(t *testing.T) {
			issuer, client, nonce, token := f.opOrigin, f.client, fresh.Nonce, issued.tokens.IDToken
			switch name {
			case "issuer":
				issuer = f.rpOrigin
			case "audience":
				client = "foreign-client"
			case "nonce":
				nonce = random(t)
			case "signature":
				parts := strings.Split(token, ".")
				if len(parts) != 3 {
					t.Fatal("actual signed token shape invalid")
				}
				if parts[2][0] == 'A' {
					parts[2] = "B" + parts[2][1:]
				} else {
					parts[2] = "A" + parts[2][1:]
				}
				token = strings.Join(parts, ".")
			}
			f.evidence.record(token, nonce)
			verifier := relyingparty.NewIDTokenVerifier(issuer, client, remote.IDTokenVerifier().KeySet, relyingparty.WithIssuedAtOffset(0), relyingparty.WithSupportedSigningAlgorithms("RS256"), relyingparty.WithNonce(func(context.Context) string { return nonce }))
			if _, err := relyingparty.VerifyTokens[*oidc.IDTokenClaims](f.ctx, issued.tokens.AccessToken, token, verifier); err == nil {
				t.Fatal("actual ID token verifier accepted foreign or altered credential")
			}
		})
	}
}

func TestLiveOPSigningRotationAndActualExpiry(t *testing.T) {
	f := newProtocolFixture(t, func(p *providerPolicy) { p.IDLifetime = 10 * time.Second })
	firstTrace := f.issueCode(t, nil)
	response := f.request(t, f.browser, "GET", firstTrace.Callback, nil, "")
	responseBody(t, response, 200)
	first := f.rpResult(t)
	oldID := f.config.SigningID
	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal("private replacement signing key unavailable")
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(signing)
	if err != nil {
		t.Fatal("private replacement signing key invalid")
	}
	f.config.SigningFile = filepath.Join(f.folder, "replacement-signing-key.pem")
	privateWrite(t, f.config.SigningFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
	clear(encoded)
	public, err := x509.MarshalPKIXPublicKey(&signing.PublicKey)
	if err != nil {
		t.Fatal("replacement public key invalid")
	}
	publicFile := filepath.Join(f.folder, "replacement-signing-public.pem")
	privateWrite(t, publicFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	f.config.SigningID = "signing-replacement"
	f.config.PublicFiles[f.config.SigningID] = publicFile
	f.restart(t)
	response = f.request(t, f.serverClient, "GET", f.opOrigin+"/keys", nil, "")
	var keys struct {
		Keys []struct {
			ID string `json:"kid"`
		}
	}
	if json.Unmarshal(responseBody(t, response, 200), &keys) != nil || len(keys.Keys) != 2 {
		t.Fatal("overlapping public signing keys not published")
	}
	// A fresh RP has no old-key cache and must fetch the retained public key.
	f.reloadRP(t, f.secret)
	remote := f.remote()
	ctx := context.WithValue(f.ctx, rpNonceContext{}, firstTrace.Nonce)
	if _, err = relyingparty.VerifyTokens[*oidc.IDTokenClaims](ctx, first.tokens.AccessToken, first.tokens.IDToken, remote.IDTokenVerifier()); err != nil {
		t.Fatal("retained signing key no longer verifies unexpired token")
	}
	info, err := relyingparty.Userinfo[*oidc.UserInfo](f.ctx, first.tokens.AccessToken, first.tokens.TokenType, f.subject, remote)
	if err != nil || info.Subject != f.subject {
		t.Fatal("signing rotation changed opaque token authority")
	}
	secondTrace := f.issueCode(t, nil)
	response = f.request(t, f.browser, "GET", secondTrace.Callback, nil, "")
	responseBody(t, response, 200)
	second := f.rpResult(t)
	// Wait for the actual signed expiration without replacing the verifier clock.
	expiration := first.tokens.IDTokenClaims.GetExpiration()
	if remaining := time.Until(expiration.Add(100 * time.Millisecond)); remaining <= 0 || remaining > 11*time.Second {
		t.Fatal("bounded actual signing-key overlap not established")
	} else {
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-f.ctx.Done():
			t.Fatal("actual token expiration exceeded fixture deadline")
		}
	}
	if _, err = relyingparty.VerifyTokens[*oidc.IDTokenClaims](ctx, first.tokens.AccessToken, first.tokens.IDToken, remote.IDTokenVerifier()); !errors.Is(err, oidc.ErrExpired) {
		t.Fatal("actual expired ID token not refused before retiring public key")
	}
	delete(f.config.PublicFiles, oldID)
	f.restart(t)
	response = f.request(t, f.serverClient, "GET", f.opOrigin+"/keys", nil, "")
	keys.Keys = nil
	if json.Unmarshal(responseBody(t, response, 200), &keys) != nil || len(keys.Keys) != 1 || keys.Keys[0].ID != f.config.SigningID {
		t.Fatal("expired old signing key retained in published set")
	}
	thirdTrace := f.issueCode(t, nil)
	response = f.request(t, f.browser, "GET", thirdTrace.Callback, nil, "")
	responseBody(t, response, 200)
	third := f.rpResult(t)
	if second.subject != f.subject || third.subject != f.subject || third.tokens.IDToken == second.tokens.IDToken {
		t.Fatal("signing replacement did not preserve fresh stable identity")
	}
}

func TestLiveOPActualCodeExpiryAndHTTPBodyBudget(t *testing.T) {
	f := newProtocolFixture(t, func(p *providerPolicy) { p.CodeLifetime = 2 * time.Second })
	expired := f.issueCode(t, nil)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND code_expires_at<=clock_timestamp()`, expired.RequestID) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE id=$1 AND code_expires_at<=clock_timestamp()`, expired.RequestID) != 1 {
		t.Fatal("actual database code expiration not observed")
	}
	status, fields := f.exchange(t, expired, f.secret, expired.Verifier, f.rpOrigin+"/callback", f.client)
	if status != 400 {
		t.Fatal("actual expired code accepted")
	}
	noTokenFields(t, fields)
	fresh := f.issueCode(t, nil)
	body := url.Values{"grant_type": {"authorization_code"}, "code": {fresh.Code}, "redirect_uri": {f.rpOrigin + "/callback"}, "code_verifier": {fresh.Verifier}, "padding": {strings.Repeat("a", f.config.Policy.Request.MaxBody)}}
	r, err := http.NewRequestWithContext(f.ctx, "POST", f.opOrigin+"/oauth/token", strings.NewReader(body.Encode()))
	if err != nil {
		t.Fatal("bounded HTTP request construction failed")
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth(url.QueryEscape(f.client), url.QueryEscape(f.secret))
	response, err := f.serverClient.Do(r)
	if err != nil {
		t.Fatal("oversized form HTTPS failed")
	}
	if json.Unmarshal(responseBody(t, response, 400), &fields) != nil {
		t.Fatal("oversized form refusal invalid")
	}
	noTokenFields(t, fields)
	if f.scalar(t, `SELECT count(*) FROM op_fixture_tokens`) != 0 || f.scalar(t, `SELECT count(*) FROM op_fixture_requests WHERE consumed_at IS NOT NULL`) != 0 {
		t.Fatal("expired or oversized request changed issuance")
	}
	status, fields = f.exchange(t, fresh, f.secret, fresh.Verifier, f.rpOrigin+"/callback", f.client)
	if status != 200 || fields["access_token"] == nil {
		t.Fatal("explicit exchange after oversized body failed")
	}
}
