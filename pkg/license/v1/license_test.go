package v1

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func claimFixture() Claims {
	return Claims{FormatVersion, "11111111-1111-4111-8111-111111111111", "Synthetic customer \u5ba2\u6237", "22222222-2222-4222-8222-222222222222", "2026-10-19T00:00:00Z", "2026-10-19T00:00:00Z", "2027-10-19T00:00:00Z", "2027-11-19T00:00:00Z", "", WholePlatformScope}
}
func keys(t *testing.T) (PrivateKey, Trust) {
	t.Helper()
	p, k, e := GenerateKey("synthetic-issuer")
	if e != nil {
		t.Fatal(e)
	}
	return p, k
}
func TestLicenseActualEd25519ContextSignatureUnicodeAndDeterministicArtifact(t *testing.T) {
	private, trust := keys(t)
	c := claimFixture()
	raw, e := Issue(c, private)
	if e != nil {
		t.Fatal(e)
	}
	v, e := Verify(raw, trust)
	if e != nil || v.Claims != c || v.KeyID != trust.KeyID || v.SHA256 != Digest(raw) || len(v.Fingerprint) != 64 || !bytes.Equal(v.Artifact, raw) {
		t.Fatal("actual signed artifact", v, e)
	}
	again, e := Issue(c, private)
	if e != nil || !bytes.Equal(raw, again) {
		t.Fatal("same exact signed claims changed", e)
	}
	for _, pair := range []struct {
		raw   []byte
		parse func([]byte) error
	}{
		{canonical(private), func(b []byte) error { _, e := ParsePrivate(b); return e }},
		{canonical(trust), func(b []byte) error { _, e := ParseTrust(b); return e }},
		{canonical(c), func(b []byte) error { _, e := ParseClaims(b); return e }},
	} {
		if pair.parse(pair.raw) != nil {
			t.Fatal("closed file roundtrip")
		}
	}
	public, e := Public(private)
	if e != nil || public != trust {
		t.Fatal("separate public trust", e)
	}
	if _, e := ParseTrust(canonical(private)); e == nil {
		t.Fatal("private seed accepted as customer trust")
	}
}

func TestLicenseTamperWrongTrustAndSignatureDomainCannotVerify(t *testing.T) {
	private, trust := keys(t)
	raw, _ := Issue(claimFixture(), private)
	var original Artifact
	if json.Unmarshal(raw, &original) != nil {
		t.Fatal("artifact")
	}
	for _, fault := range []string{"signature", "payload", "key-id", "algorithm", "version", "plain-ed25519", "wrong-context", "noncanonical-signed"} {
		t.Run(fault, func(t *testing.T) {
			a := original
			payload, _ := decoded(a.Payload, MaximumClaimsBytes)
			key, _ := private.key()
			switch fault {
			case "signature":
				a.Signature = encoded(make([]byte, ed25519.SignatureSize))
			case "payload":
				a.Payload = encoded(bytes.Replace(payload, []byte("whole_platform"), []byte("module_tier"), 1))
			case "key-id":
				a.KeyID = "other"
			case "algorithm":
				a.Algorithm = "Ed25519"
			case "version":
				a.FormatVersion = 2
			case "plain-ed25519":
				a.Signature = encoded(ed25519.Sign(key, payload))
			case "wrong-context":
				signature, _ := key.Sign(nil, payload, &ed25519.Options{Context: "another-product"})
				a.Signature = encoded(signature)
			case "noncanonical-signed":
				payload = append(payload, '\n')
				signature, _ := key.Sign(nil, payload, &ed25519.Options{Context: SignatureContext})
				a.Payload, a.Signature = encoded(payload), encoded(signature)
			}
			if _, e := Verify(canonical(a), trust); e == nil {
				t.Fatal("untrusted artifact accepted")
			}
		})
	}
	_, wrong := keys(t)
	if _, e := Verify(raw, wrong); e == nil {
		t.Fatal("another public key with same key ID accepted")
	}
}

func TestLicenseClosedJSONRejectsAliasesNullDuplicateMissingInvalidUTF8AndTrailing(t *testing.T) {
	private, trust := keys(t)
	raw, _ := Issue(claimFixture(), private)
	for _, bad := range [][]byte{
		[]byte("null"), []byte("[]"), bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":null`), 1),
		bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"FORMAT_VERSION":1`), 1),
		bytes.Replace(raw, []byte(`"format_version":1`), []byte(`"format_version":1,"format_version":1`), 1),
		bytes.Replace(raw, []byte(`"format_version":1,`), nil, 1), append(append([]byte{}, raw...), []byte(" {}")...),
		append(append([]byte{}, raw...), 0xff), append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), MaximumArtifactBytes)...),
		bytes.Replace(raw, []byte(`"algorithm":`), []byte(`"unknown":"value","algorithm":`), 1),
	} {
		if _, e := Verify(bad, trust); e == nil {
			t.Fatal("invalid outer shape accepted")
		}
	}
	for _, raw := range [][]byte{canonical(trust), canonical(private), canonical(claimFixture())} {
		for _, field := range []string{"format_version"} {
			bad := bytes.Replace(raw, []byte(`"`+field+`":1`), []byte(`"`+field+`":true`), 1)
			var e error
			switch {
			case bytes.Contains(raw, []byte(`"seed"`)):
				_, e = ParsePrivate(bad)
			case bytes.Contains(raw, []byte(`"public_key"`)):
				_, e = ParseTrust(bad)
			default:
				_, e = ParseClaims(bad)
			}
			if e == nil {
				t.Fatal("wrong scalar type accepted")
			}
		}
	}
}

func TestLicenseTimeScopeIdentityAndCustomerBoundaries(t *testing.T) {
	for _, fault := range []string{"nil-id", "uppercase-id", "replaces-self", "wrong-scope", "blank-customer", "long-customer", "control-customer", "fractional-time", "offset-time", "zero-time", "backward-issue", "empty-window", "backward-grace"} {
		t.Run(fault, func(t *testing.T) {
			c := claimFixture()
			switch fault {
			case "nil-id":
				c.DeploymentID = "00000000-0000-0000-0000-000000000000"
			case "uppercase-id":
				c.LicenseID = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
			case "replaces-self":
				c.ReplacesLicenseID = c.LicenseID
			case "wrong-scope":
				c.Scope = "training_only"
			case "blank-customer":
				c.CustomerReference = " "
			case "long-customer":
				c.CustomerReference = strings.Repeat("\u5ba2", MaximumCustomerRunes+1)
			case "control-customer":
				c.CustomerReference = "synthetic\ncustomer"
			case "fractional-time":
				c.NotBefore = "2026-10-19T00:00:00.000Z"
			case "offset-time":
				c.NotBefore = "2026-10-19T00:00:00+00:00"
			case "zero-time":
				c.IssuedAt = "0001-01-01T00:00:00Z"
			case "backward-issue":
				c.IssuedAt = c.ExpiresAt
			case "empty-window":
				c.NotBefore = c.ExpiresAt
			case "backward-grace":
				c.GraceUntil = c.NotBefore
			}
			if c.Validate() == nil {
				t.Fatal("invalid claim accepted")
			}
		})
	}
	c := claimFixture()
	c.CustomerReference = strings.Repeat("\u5ba2", MaximumCustomerRunes)
	c.GraceUntil = c.ExpiresAt
	if e := c.Validate(); e != nil {
		t.Fatal("Unicode rune boundary or zero grace refused", e)
	}
}

func TestLicenseKeyAndBase64BoundariesNeverPanicOrAcceptNoncanonicalEncoding(t *testing.T) {
	private, trust := keys(t)
	for _, value := range []string{"", "AA", encoded(make([]byte, 31)), trust.PublicKey + "=", trust.PublicKey + "\n"} {
		bad := trust
		bad.PublicKey = value
		if _, e := ParseTrust(canonical(bad)); e == nil {
			t.Fatal("invalid public key length/base64 accepted")
		}
	}
	for _, id := range []string{"", "Upper", strings.Repeat("a", 65), "secret\nkey"} {
		if _, _, e := GenerateKey(id); e == nil {
			t.Fatal("invalid key ID accepted")
		}
	}
	seed, _ := base64.RawURLEncoding.DecodeString(private.Seed)
	if len(seed) != 32 {
		t.Fatal("wrong seed encoding")
	}
	private.Seed = encoded(seed[:31])
	if _, e := Issue(claimFixture(), private); e == nil {
		t.Fatal("bad private seed reached signing API")
	}
}

func TestLicenseClaimsUnicodeEscapesRefuseSubstitutionAndPreserveValidContent(t *testing.T) {
	c := claimFixture()
	original, _ := json.Marshal(c.CustomerReference)
	field := append([]byte(`"customer_reference":`), original...)
	withCustomer := func(literal string) []byte {
		return bytes.Replace(canonical(c), field, []byte(`"customer_reference":`+literal), 1)
	}
	for _, literal := range []string{`"\ud800"`, `"\udc00"`, `"\ud800\ud800"`, `"\ud800\u0041"`, `"\ud800x"`, `"\ud800\\udc00"`, `"\ud83d\ude00\udc00"`} {
		if _, e := ParseClaims(withCustomer(literal)); e == nil || e.Error() != "LICENSE_ARTIFACT_INVALID" {
			t.Fatal("malformed surrogate escaped the closed claim boundary")
		}
	}
	private, trust := keys(t)
	for _, value := range []struct{ literal, customer string }{
		{`"\ud83d\ude00"`, "😀"},
		{`"\uD83D\uDE00"`, "😀"},
		{`"genuine \ufffd"`, "genuine �"},
		{`"literal \\ud800"`, `literal \ud800`},
		{`"\ufeffSynthetic"`, "\ufeffSynthetic"},
		{"\"\u5ba2\u6237 \u0627\u0644\u0639\u0631\u0628\u064a\u0629 😀\"", "\u5ba2\u6237 \u0627\u0644\u0639\u0631\u0628\u064a\u0629 😀"},
	} {
		parsed, e := ParseClaims(withCustomer(value.literal))
		if e != nil || parsed.CustomerReference != value.customer {
			t.Fatal("valid customer content changed or was rejected")
		}
		raw, e := Issue(parsed, private)
		if e != nil {
			t.Fatal(e)
		}
		verified, e := Verify(raw, trust)
		if e != nil || verified.Claims.CustomerReference != value.customer {
			t.Fatal("valid signed claim roundtrip changed")
		}
	}
}

func TestInspectNamesTheFirstCheckAFileFails(t *testing.T) {
	private, trust := keys(t)
	other, _, e := GenerateKey("another-issuer")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := Issue(claimFixture(), private)
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := Issue(claimFixture(), other)
	if e != nil {
		t.Fatal(e)
	}
	var tampered Artifact
	if e := json.Unmarshal(raw, &tampered); e != nil {
		t.Fatal(e)
	}
	bad := []byte(tampered.Signature)
	bad[0] = map[bool]byte{true: 'B', false: 'A'}[bad[0] == 'A']
	tampered.Signature = string(bad)
	// Terms that break the format, correctly signed: Issue refuses them, so sign by hand.
	seed, e := private.key()
	if e != nil {
		t.Fatal(e)
	}
	broken := claimFixture()
	broken.Scope = "per_feature"
	payload := canonical(broken)
	signature, e := seed.Sign(nil, payload, &ed25519.Options{Hash: 0, Context: SignatureContext})
	if e != nil {
		t.Fatal(e)
	}
	wrongTerms := canonical(Artifact{FormatVersion, Algorithm, private.KeyID, encoded(payload), encoded(signature)})

	for _, item := range []struct {
		name    string
		raw     []byte
		refusal Refusal
		keyID   string
	}{
		{"not JSON", []byte("not a license"), RefusalUnreadable, ""},
		{"another format", []byte(`{"format_version":2}`), RefusalUnreadable, ""},
		{"another issuer", foreign, RefusalKey, "another-issuer"},
		{"changed after signing", canonical(tampered), RefusalSignature, trust.KeyID},
		{"signed but malformed terms", wrongTerms, RefusalTerms, trust.KeyID},
	} {
		t.Run(item.name, func(t *testing.T) {
			found, e := Inspect(item.raw, trust)
			if e != nil || found.Refusal != item.refusal || found.KeyID != item.keyID || found.Verified.SHA256 != "" {
				t.Fatal(found, e)
			}
			if _, e := Verify(item.raw, trust); e == nil || e.Error() != "LICENSE_ARTIFACT_INVALID" {
				t.Fatal("verify must keep its one public refusal", e)
			}
		})
	}
	found, e := Inspect(raw, trust)
	if e != nil || found.Refusal != "" || found.KeyID != trust.KeyID || found.Verified.Claims != claimFixture() {
		t.Fatal(found, e)
	}
}
