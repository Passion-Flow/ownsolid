package boundary

import (
	"crypto/subtle"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zitadel/oidc/v3/pkg/op"
)

// BuildAuthRequestCode reads only GetID; this fixture supplies no auth/session
// implementation and does not qualify authorization or a serving Storage.
type requestIDFixture struct {
	op.AuthRequest
	id string
}

func (f requestIDFixture) GetID() string { return f.id }

func TestSDKAuthorizationCodeAndBearerPurpose(t *testing.T) {
	codeKey, bearerKey := key(t, "code-key"), key(t, "bearer-key")
	c, err := newProviderCrypto(cp(), codeKey.ID, []cryptoKey{codeKey}, bearerKey.ID, []cryptoKey{bearerKey})
	if err != nil {
		t.Fatal(err)
	}
	id := "ar_" + random(t)
	code, err := op.BuildAuthRequestCode(requestIDFixture{id: id}, c)
	if err != nil || len(code) == 0 {
		t.Fatal("actual SDK code builder failed", err)
	}
	plain, err := op.NewAES256GCMCrypto(codeKey.Value, codeKey.ID).Decrypt(code)
	if err != nil || plain != id {
		t.Fatal("code used wrong key/payload", err)
	}
	if out, e := c.Decrypt(code); !errors.Is(e, errToken) || out != "" {
		t.Fatal("code decrypted as access bearer")
	}
	bearerPlain := payload(t)
	bearer, err := op.CreateBearerToken(strings.Split(bearerPlain, ":")[0], strings.Split(bearerPlain, ":")[1], c)
	if err != nil {
		t.Fatal(err)
	}
	if out, e := c.Decrypt(bearer); e != nil || out != bearerPlain {
		t.Fatal("actual SDK bearer incompatible", e)
	}
	if out, e := c.code.Decrypt(bearer); e == nil || out != "" {
		t.Fatal("bearer read by code ring")
	}
	if out, e := c.code.Encrypt(bearerPlain); e == nil || out != "" {
		t.Fatal("code encrypter accepted bearer shape")
	}
	r := httptest.NewRequest("GET", "https://op.example.invalid/userinfo", nil)
	r.Header.Set("Authorization", "Bearer "+code)
	if out, e := userInfoToken(r, c.bearer); e == nil || out != "" {
		t.Fatal("code entered UserInfo")
	}
	provider, err := op.NewOpenIDProvider("https://op.example.invalid", &op.Config{}, nil, op.WithCrypto(c))
	if err != nil {
		t.Fatal(err)
	}
	code, err = op.BuildAuthRequestCode(requestIDFixture{id: id}, provider.Crypto())
	if err != nil {
		t.Fatal("SDK constructor lost purpose composition", err)
	}
	if out, e := provider.Crypto().Decrypt(code); e == nil || out != "" {
		t.Fatal("constructor enabled code decryption")
	}
}
func TestCryptoPurposeConfigurationAndRetirement(t *testing.T) {
	codeKey, bearerKey := key(t, "code-key"), key(t, "bearer-key")
	for _, bad := range []cryptoKey{{ID: codeKey.ID, Value: bearerKey.Value}, {ID: "other", Value: codeKey.Value}} {
		if c, e := newProviderCrypto(cp(), codeKey.ID, []cryptoKey{codeKey}, bad.ID, []cryptoKey{bad}); e == nil || c != nil {
			t.Fatal("purpose key reuse accepted")
		}
	}
	c, err := newProviderCrypto(cp(), codeKey.ID, []cryptoKey{codeKey}, bearerKey.ID, []cryptoKey{bearerKey})
	if err != nil {
		t.Fatal(err)
	}
	id := "ar_" + random(t)
	code, err := c.Encrypt(id)
	if err != nil {
		t.Fatal(err)
	}
	newCodeKey := key(t, "code-next")
	rotated, err := newProviderCrypto(cp(), newCodeKey.ID, []cryptoKey{newCodeKey, codeKey}, bearerKey.ID, []cryptoKey{bearerKey})
	if err != nil {
		t.Fatal(err)
	}
	if out, e := rotated.code.Decrypt(code); e != nil || out != id {
		t.Fatal("retiring code key not retained")
	}
	retired, err := newProviderCrypto(cp(), newCodeKey.ID, []cryptoKey{newCodeKey}, bearerKey.ID, []cryptoKey{bearerKey})
	if err != nil {
		t.Fatal(err)
	}
	if out, e := retired.code.Decrypt(code); e == nil || out != "" {
		t.Fatal("retired code key still usable")
	}
	for _, invalid := range []string{"", random(t), "ar_" + random(t) + "extra", "ar_" + strings.Repeat("a", 43)} {
		if out, e := c.Encrypt(invalid); e == nil || out != "" {
			t.Fatal("unadmitted encryption payload")
		}
	}
	callerKeys := []cryptoKey{codeKey}
	owned, err := newProviderCrypto(cp(), codeKey.ID, callerKeys, bearerKey.ID, []cryptoKey{bearerKey})
	if err != nil {
		t.Fatal(err)
	}
	clear(callerKeys[0].Value[:])
	if subtle.ConstantTimeCompare(callerKeys[0].Value[:], codeKey.Value[:]) != 0 {
		code, err = owned.Encrypt(id)
		if err != nil {
			t.Fatal("caller mutated owned keys")
		}
		if got, e := owned.code.Decrypt(code); e != nil || got != id {
			t.Fatal("owned copied key changed")
		}
	} else {
		t.Fatal("random key unexpectedly zero")
	}
}
