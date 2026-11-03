package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestM01PasswordPolicyAndNormalization(t *testing.T) {
	if _, e := NewPasswords(filepath.Join(t.TempDir(), "missing")); e == nil {
		t.Fatal("missing blocklist accepted")
	}
	path := filepath.Join(t.TempDir(), "blocklist")
	os.WriteFile(path, []byte("passwordpassword\n"), 0600)
	p, e := NewPasswords(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"short", strings.Repeat("a", 129)} {
		if _, e = p.Policy(v, "alice@example.invalid", "Test"); e == nil {
			t.Fatal("policy accepted out of bounds")
		}
	}
	if _, e = p.Policy("passwordpassword", "alice@example.invalid", "Test"); e == nil || e.Error() != "PASSWORD_BLOCKED" {
		t.Fatal("blocklist not enforced")
	}
	raw := "  Unicode e\u0301 passphrase  "
	phc, e := p.Hash(context.Background(), raw)
	if e != nil {
		t.Fatal(e)
	}
	ok, e := p.Verify(context.Background(), phc, "  Unicode \u00e9 passphrase  ")
	if !ok || e != nil {
		t.Fatal("NFC verification failed")
	}
	ok, _ = p.Verify(context.Background(), phc, strings.TrimSpace(raw))
	if ok {
		t.Fatal("password was trimmed")
	}
	for _, bad := range []string{"$argon2id$v=19$m=999999999,t=2,p=1$x$x", strings.Replace(phc, "m=19456", "m=1", 1), strings.Replace(phc, "p=1", "p=0", 1)} {
		if _, e = p.Verify(context.Background(), bad, raw); e == nil {
			t.Fatal("unsafe PHC accepted")
		}
	}
}
func TestM01EmailNamePolicy(t *testing.T) {
	got, display, e := Email(" Alice.Tag+one@EXAMPLE.invalid \t")
	if e != nil || got != "alice.tag+one@example.invalid" || display != "Alice.Tag+one@EXAMPLE.invalid" {
		t.Fatal("email canonicalization")
	}
	for _, v := range []string{"\u7528\u6237@example.invalid", "a..b@example.invalid", "a@-invalid.example", "\"alice\"@example.invalid"} {
		if _, _, e = Email(v); e == nil {
			t.Fatal("unsupported email accepted")
		}
	}
	if v, e := Name(" e\u0301 <script> "); e != nil || v != "\u00e9 <script>" {
		t.Fatal("name normalization")
	}
	if _, e := Name("a\n"); e != nil {
		t.Fatal("edge whitespace should trim")
	}
	if _, e := Name("a\nb"); e == nil {
		t.Fatal("control accepted")
	}
}
func TestM01CSRFBinding(t *testing.T) {
	key := make([]byte, 32)
	now := time.Now()
	ctx, session := Token(), Token()
	token, _ := CSRF(key, "portal", ctx, session, now)
	if !VerifyCSRF(key, "portal", ctx, session, token, now) {
		t.Fatal("valid CSRF rejected")
	}
	for _, args := range [][]string{{"admin", ctx, session}, {"portal", Token(), session}, {"portal", ctx, Token()}, {"portal", ctx, ""}} {
		if VerifyCSRF(key, args[0], args[1], args[2], token, now) {
			t.Fatal("binding bypass")
		}
	}
	if VerifyCSRF(key, "portal", ctx, session, token, now.Add(16*time.Minute)) {
		t.Fatal("expired CSRF accepted")
	}
}
func TestM01HashQueueBound(t *testing.T) {
	p := &Passwords{slots: make(chan struct{}, 2), queue: make(chan struct{}, 18)}
	p.slots <- struct{}{}
	p.slots <- struct{}{}
	for i := 0; i < 18; i++ {
		p.queue <- struct{}{}
	}
	start := time.Now()
	if _, e := p.Hash(context.Background(), "Synthetic passphrase"); e == nil || e.Error() != "AUTH_CAPACITY_EXCEEDED" {
		t.Fatal("queue overflow accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("overflow was queued")
	}
}
