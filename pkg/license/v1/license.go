// Package v1 defines the offline whole-platform license wire, not commercial tiers.
package v1

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const FormatVersion = 1
const Algorithm = "Ed25519ctx"
const SignatureContext = "ProjectOne.PlatformLicense.v1"
const WholePlatformScope = "whole_platform"
const MaximumClaimsBytes = 16 << 10
const MaximumArtifactBytes = 64 << 10
const MaximumKeyBytes = 4 << 10
const MaximumCustomerRunes = 128

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Claims struct {
	FormatVersion     int    `json:"format_version"`
	LicenseID         string `json:"license_id"`
	CustomerReference string `json:"customer_reference"`
	DeploymentID      string `json:"deployment_id"`
	IssuedAt          string `json:"issued_at"`
	NotBefore         string `json:"not_before"`
	ExpiresAt         string `json:"expires_at"`
	GraceUntil        string `json:"grace_until"`
	ReplacesLicenseID string `json:"replaces_license_id"`
	Scope             string `json:"scope"`
}

type Artifact struct {
	FormatVersion int    `json:"format_version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	Payload       string `json:"payload"`
	Signature     string `json:"signature"`
}

type Trust struct {
	FormatVersion int    `json:"format_version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	PublicKey     string `json:"public_key"`
}

// PrivateKey belongs only to the separate manufacturer issuer. Customer runtime
// configuration accepts Trust; it has no seed/private-key input or signer route.
type PrivateKey struct {
	FormatVersion int    `json:"format_version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	Seed          string `json:"seed"`
}

type Verified struct {
	Claims      Claims
	KeyID       string
	SHA256      string
	Fingerprint string
	Artifact    []byte
}

func invalid() error { return errors.New("LICENSE_ARTIFACT_INVALID") }
func ValidID(value string) bool {
	return uuidPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}
func ValidKeyID(value string) bool { return keyPattern.MatchString(value) }
func Digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// ClosedJSON requires every exact declared field once, without nulls, aliases,
// unknown fields or trailing values. All wire types here have flat scalar fields.
func closedJSON(raw []byte, target any, maximum int) error {
	if len(raw) == 0 || len(raw) > maximum || !validJSONUnicode(raw) {
		return invalid()
	}
	kind := reflect.TypeOf(target).Elem()
	fields := map[string]bool{}
	for i := 0; i < kind.NumField(); i++ {
		fields[kind.Field(i).Tag.Get("json")] = false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, e := decoder.Token()
	if e != nil || token != json.Delim('{') {
		return invalid()
	}
	for decoder.More() {
		token, e = decoder.Token()
		name, ok := token.(string)
		seen, known := fields[name]
		if e != nil || !ok || !known || seen {
			return invalid()
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
		fields[name] = true
	}
	if token, e = decoder.Token(); e != nil || token != json.Delim('}') {
		return invalid()
	}
	if _, e = decoder.Token(); e != io.EOF {
		return invalid()
	}
	for _, seen := range fields {
		if !seen {
			return invalid()
		}
	}
	if json.Unmarshal(raw, target) != nil {
		return invalid()
	}
	return nil
}

func canonical(raw any) []byte {
	b, _ := json.Marshal(raw) // The closed scalar structs cannot fail marshaling.
	return b
}
func encoded(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
func decoded(text string, maximum int) ([]byte, error) {
	if len(text) > base64.RawURLEncoding.EncodedLen(maximum) {
		return nil, invalid()
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(text)
	if e != nil || len(raw) > maximum || encoded(raw) != text {
		return nil, invalid()
	}
	return raw, nil
}
func Stamp(value string) (time.Time, error) {
	t, e := time.Parse(time.RFC3339, value)
	if e != nil || t.IsZero() || t.Unix() < 0 || t.UTC().Format(time.RFC3339) != value {
		return time.Time{}, invalid()
	}
	return t, nil
}
func (c Claims) Validate() error {
	if c.FormatVersion != FormatVersion || !ValidID(c.LicenseID) || !ValidID(c.DeploymentID) || c.ReplacesLicenseID != "" && (!ValidID(c.ReplacesLicenseID) || c.ReplacesLicenseID == c.LicenseID) || c.Scope != WholePlatformScope || !utf8.ValidString(c.CustomerReference) || c.CustomerReference != strings.TrimSpace(c.CustomerReference) || c.CustomerReference == "" || utf8.RuneCountInString(c.CustomerReference) > MaximumCustomerRunes || strings.ContainsFunc(c.CustomerReference, unicode.IsControl) {
		return invalid()
	}
	issued, ie := Stamp(c.IssuedAt)
	start, se := Stamp(c.NotBefore)
	expires, ee := Stamp(c.ExpiresAt)
	grace, ge := Stamp(c.GraceUntil)
	if ie != nil || se != nil || ee != nil || ge != nil || issued.After(start) || !start.Before(expires) || expires.After(grace) {
		return invalid()
	}
	return nil
}

func ParseClaims(raw []byte) (Claims, error) {
	var c Claims
	if closedJSON(raw, &c, MaximumClaimsBytes) != nil || c.Validate() != nil {
		return Claims{}, invalid()
	}
	return c, nil
}

func ParseTrust(raw []byte) (Trust, error) {
	var t Trust
	if closedJSON(raw, &t, MaximumKeyBytes) != nil {
		return Trust{}, errors.New("LICENSE_TRUST_INVALID")
	}
	if _, e := t.key(); e != nil {
		return Trust{}, e
	}
	return t, nil
}
func (t Trust) key() (ed25519.PublicKey, error) {
	raw, e := decoded(t.PublicKey, ed25519.PublicKeySize)
	if t.FormatVersion != FormatVersion || t.Algorithm != Algorithm || !ValidKeyID(t.KeyID) || e != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("LICENSE_TRUST_INVALID")
	}
	return ed25519.PublicKey(raw), nil
}
func (t Trust) Fingerprint() (string, error) {
	key, e := t.key()
	if e != nil {
		return "", e
	}
	return Digest(key), nil
}
func ParsePrivate(raw []byte) (PrivateKey, error) {
	var p PrivateKey
	if closedJSON(raw, &p, MaximumKeyBytes) != nil {
		return PrivateKey{}, errors.New("LICENSE_ISSUER_KEY_INVALID")
	}
	if _, e := p.key(); e != nil {
		return PrivateKey{}, e
	}
	return p, nil
}
func (p PrivateKey) key() (ed25519.PrivateKey, error) {
	raw, e := decoded(p.Seed, ed25519.SeedSize)
	if p.FormatVersion != FormatVersion || p.Algorithm != Algorithm || !ValidKeyID(p.KeyID) || e != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("LICENSE_ISSUER_KEY_INVALID")
	}
	return ed25519.NewKeyFromSeed(raw), nil
}
func GenerateKey(keyID string) (PrivateKey, Trust, error) {
	if !ValidKeyID(keyID) {
		return PrivateKey{}, Trust{}, errors.New("LICENSE_ISSUER_KEY_ID_INVALID")
	}
	public, private, e := ed25519.GenerateKey(nil)
	if e != nil {
		return PrivateKey{}, Trust{}, errors.New("LICENSE_ISSUER_ENTROPY_UNAVAILABLE")
	}
	return PrivateKey{FormatVersion, Algorithm, keyID, encoded(private.Seed())}, Trust{FormatVersion, Algorithm, keyID, encoded(public)}, nil
}
func Public(p PrivateKey) (Trust, error) {
	key, e := p.key()
	if e != nil {
		return Trust{}, e
	}
	return Trust{FormatVersion, Algorithm, p.KeyID, encoded(key.Public().(ed25519.PublicKey))}, nil
}
func Issue(c Claims, p PrivateKey) ([]byte, error) {
	if c.Validate() != nil {
		return nil, invalid()
	}
	key, e := p.key()
	if e != nil {
		return nil, e
	}
	payload := canonical(c)
	if len(payload) > MaximumClaimsBytes {
		return nil, invalid()
	}
	signature, e := key.Sign(nil, payload, &ed25519.Options{Hash: crypto.Hash(0), Context: SignatureContext})
	if e != nil {
		return nil, errors.New("LICENSE_ISSUER_SIGNING_FAILED")
	}
	return canonical(Artifact{FormatVersion, Algorithm, p.KeyID, encoded(payload), encoded(signature)}), nil
}

func Verify(raw []byte, trust Trust) (Verified, error) {
	public, e := trust.key()
	if e != nil {
		return Verified{}, e
	}
	var a Artifact
	if closedJSON(raw, &a, MaximumArtifactBytes) != nil || a.FormatVersion != FormatVersion || a.Algorithm != Algorithm || a.KeyID != trust.KeyID {
		return Verified{}, invalid()
	}
	payload, pe := decoded(a.Payload, MaximumClaimsBytes)
	signature, se := decoded(a.Signature, ed25519.SignatureSize)
	if pe != nil || se != nil || len(signature) != ed25519.SignatureSize || ed25519.VerifyWithOptions(public, payload, signature, &ed25519.Options{Hash: crypto.Hash(0), Context: SignatureContext}) != nil {
		return Verified{}, invalid()
	}
	claims, e := ParseClaims(payload)
	if e != nil || !bytes.Equal(payload, canonical(claims)) {
		return Verified{}, invalid()
	}
	artifact := canonical(a)
	return Verified{Claims: claims, KeyID: a.KeyID, SHA256: Digest(artifact), Fingerprint: Digest(public), Artifact: artifact}, nil
}
