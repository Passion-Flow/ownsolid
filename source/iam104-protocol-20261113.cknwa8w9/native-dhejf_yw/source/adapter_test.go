package samlprofile

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
)

type fixture struct {
	key  *rsa.PrivateKey
	cert *x509.Certificate
	now  time.Time
	cfg  configuration
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Private SAML IdP"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{key: key, cert: cert, now: now}
	f.cfg = configuration{SP: "https://console.iam104.test/metadata", ACS: "https://console.iam104.test/saml/acs", Issuer: "https://keycloak.iam104.test/realms/iam104", Endpoint: "https://keycloak.iam104.test/realms/iam104/protocol/saml", TrustedUntil: now.Add(time.Hour), Clock: dsig.NewFakeClockAt(now), Limits: limits{128 << 10, 24, 2048, 32}, MaxAge: 60 * time.Second, Skew: 30 * time.Second, MaxAssertionLifetime: 5 * time.Minute}
	f.cfg.Metadata = f.metadata(t, cert)
	return f
}
func (f *fixture) metadata(t *testing.T, certs ...*x509.Certificate) []byte {
	t.Helper()
	var keys []saml.KeyDescriptor
	for _, c := range certs {
		keys = append(keys, saml.KeyDescriptor{Use: "signing", KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: base64.StdEncoding.EncodeToString(c.Raw)}}}}})
	}
	meta := saml.EntityDescriptor{EntityID: f.cfg.Issuer, ValidUntil: f.now.Add(time.Hour), IDPSSODescriptors: []saml.IDPSSODescriptor{{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{ProtocolSupportEnumeration: protocolNS, KeyDescriptors: keys}}, SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: f.cfg.Endpoint}}}}}
	raw, e := xml.Marshal(meta)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func child(p *etree.Element, name, text string, attrs ...string) *etree.Element {
	v := p.CreateElement(name)
	v.SetText(text)
	for i := 0; i < len(attrs); i += 2 {
		v.CreateAttr(attrs[i], attrs[i+1])
	}
	return v
}
func (f *fixture) assertion() *etree.Element {
	a := etree.NewElement("saml:Assertion")
	a.CreateAttr("xmlns:saml", assertionNS)
	a.CreateAttr("ID", "_assertion_private")
	a.CreateAttr("Version", "2.0")
	a.CreateAttr("IssueInstant", f.now.Format(time.RFC3339))
	child(a, "saml:Issuer", f.cfg.Issuer)
	subject := child(a, "saml:Subject", "")
	child(subject, "saml:NameID", "Opaque:Subject/+Case", "Format", string(saml.PersistentNameIDFormat), "NameQualifier", f.cfg.Issuer, "SPNameQualifier", f.cfg.SP)
	confirmation := child(subject, "saml:SubjectConfirmation", "", "Method", "urn:oasis:names:tc:SAML:2.0:cm:bearer")
	child(confirmation, "saml:SubjectConfirmationData", "", "InResponseTo", "_request_private", "Recipient", f.cfg.ACS, "NotOnOrAfter", f.now.Add(time.Minute).Format(time.RFC3339))
	conditions := child(a, "saml:Conditions", "", "NotBefore", f.now.Add(-time.Second).Format(time.RFC3339), "NotOnOrAfter", f.now.Add(time.Minute).Format(time.RFC3339))
	audience := child(conditions, "saml:AudienceRestriction", "")
	child(audience, "saml:Audience", f.cfg.SP)
	auth := child(a, "saml:AuthnStatement", "", "AuthnInstant", f.now.Format(time.RFC3339), "SessionIndex", "_session_private")
	context := child(auth, "saml:AuthnContext", "")
	child(context, "saml:AuthnContextClassRef", "urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport")
	return a
}
func (f *fixture) sign(t *testing.T, el *etree.Element, method string) *etree.Element {
	t.Helper()
	ctx, e := dsig.NewSigningContext(f.key, [][]byte{f.cert.Raw})
	if e != nil {
		t.Fatal(e)
	}
	ctx.IdAttribute = "ID"
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if e = ctx.SetSignatureMethod(method); e != nil {
		t.Fatal(e)
	}
	sig, e := ctx.ConstructSignature(el, true)
	if e != nil {
		t.Fatal(e)
	}
	signed := el.Copy()
	signed.InsertChildAt(1, sig)
	return signed
}
func (f *fixture) response(a *etree.Element) *etree.Element {
	r := etree.NewElement("samlp:Response")
	r.CreateAttr("xmlns:samlp", protocolNS)
	r.CreateAttr("xmlns:saml", assertionNS)
	r.CreateAttr("ID", "_response_private")
	r.CreateAttr("Version", "2.0")
	r.CreateAttr("IssueInstant", f.now.Format(time.RFC3339))
	r.CreateAttr("InResponseTo", "_request_private")
	r.CreateAttr("Destination", f.cfg.ACS)
	child(r, "saml:Issuer", f.cfg.Issuer)
	status := child(r, "samlp:Status", "")
	child(status, "samlp:StatusCode", "", "Value", saml.StatusSuccess)
	r.AddChild(a)
	return r
}
func encoded(t *testing.T, root *etree.Element) string {
	t.Helper()
	d := etree.NewDocument()
	d.SetRoot(root)
	raw, e := d.WriteToBytes()
	if e != nil {
		t.Fatal(e)
	}
	return base64.StdEncoding.EncodeToString(raw)
}
func makeVerifier(t *testing.T, c configuration) *verifier {
	t.Helper()
	v, e := newVerifier(c)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func mustRefuse(t *testing.T, v *verifier, raw string, request string) {
	t.Helper()
	p, e := v.verify(raw, request)
	if !errors.Is(e, refused) || p.AssertionID != "" || p.Subject.Value != "" {
		t.Fatal("invalid SAML response yielded proof")
	}
}

func TestSignedPersistentSubject(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	a := f.assertion()
	a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", f.now.Add(30*time.Second).Format(time.RFC3339))
	raw := encoded(t, f.response(f.sign(t, a, rsaSHA256)))
	p, e := v.verify(raw, "_request_private")
	if e != nil || p.Subject.Value != "Opaque:Subject/+Case" || p.Subject.NameQualifier != f.cfg.Issuer || p.Subject.SPNameQualifier != f.cfg.SP || !p.ExpiresAt.Equal(f.now.Add(30*time.Second)) {
		t.Fatal("persistent subject or session bound failed", e)
	}
	if !v.sp.RejectSHA1Signatures || !v.sp.RejectExpiredIDPMetadata || !v.sp.RejectUnknownConditions || v.sp.AllowIDPInitiated || v.sp.IgnoreCertificateExpiry || v.sp.SignatureVerifier != nil {
		t.Fatal("constructor weakened protocol invariants")
	}
}
func TestCorrectlySignedClaimRefusals(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	cases := map[string]func(*etree.Element){
		"issuer": func(a *etree.Element) { a.SelectElement("Issuer").SetText("https://other.iam104.test") },
		"audience": func(a *etree.Element) {
			a.FindElement("./Conditions/AudienceRestriction/Audience").SetText("https://other.iam104.test")
		},
		"recipient": func(a *etree.Element) {
			a.FindElement("./Subject/SubjectConfirmation/SubjectConfirmationData").CreateAttr("Recipient", "https://other.iam104.test/acs")
		},
		"request": func(a *etree.Element) {
			a.FindElement("./Subject/SubjectConfirmation/SubjectConfirmationData").CreateAttr("InResponseTo", "_other_request")
		},
		"expired": func(a *etree.Element) {
			a.SelectElement("Conditions").CreateAttr("NotOnOrAfter", f.now.Add(-time.Second).Format(time.RFC3339))
		},
		"future": func(a *etree.Element) {
			a.SelectElement("Conditions").CreateAttr("NotBefore", f.now.Add(time.Minute).Format(time.RFC3339))
		},
		"old-issue":    func(a *etree.Element) { a.CreateAttr("IssueInstant", f.now.Add(-61*time.Second).Format(time.RFC3339)) },
		"future-issue": func(a *etree.Element) { a.CreateAttr("IssueInstant", f.now.Add(31*time.Second).Format(time.RFC3339)) },
		"long-window": func(a *etree.Element) {
			a.SelectElement("Conditions").CreateAttr("NotOnOrAfter", f.now.Add(6*time.Minute).Format(time.RFC3339))
		},
		"email-name": func(a *etree.Element) {
			a.FindElement("./Subject/NameID").CreateAttr("Format", string(saml.EmailAddressNameIDFormat))
		},
		"transient-name": func(a *etree.Element) {
			a.FindElement("./Subject/NameID").CreateAttr("Format", string(saml.TransientNameIDFormat))
		},
		"qualifier": func(a *etree.Element) {
			a.FindElement("./Subject/NameID").CreateAttr("NameQualifier", "https://other.iam104.test")
		},
		"sp-qualifier": func(a *etree.Element) {
			a.FindElement("./Subject/NameID").CreateAttr("SPNameQualifier", "https://other.iam104.test")
		},
		"unknown-condition": func(a *etree.Element) { child(a.SelectElement("Conditions"), "saml:Condition", "") },
		"missing-authn":     func(a *etree.Element) { a.RemoveChild(a.SelectElement("AuthnStatement")) },
		"duplicate-name": func(a *etree.Element) {
			subj := a.SelectElement("Subject")
			subj.AddChild(subj.SelectElement("NameID").Copy())
		},
		"duplicate-confirmation": func(a *etree.Element) {
			subj := a.SelectElement("Subject")
			subj.AddChild(subj.SelectElement("SubjectConfirmation").Copy())
		},
		"expired-session": func(a *etree.Element) {
			a.SelectElement("AuthnStatement").CreateAttr("SessionNotOnOrAfter", f.now.Add(-time.Second).Format(time.RFC3339))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			a := f.assertion()
			mutate(a)
			mustRefuse(t, v, encoded(t, f.response(f.sign(t, a, rsaSHA256))), "_request_private")
		})
	}
}
func TestSignedWrappingAndAlgorithmRefusals(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	for _, method := range []string{dsig.RSASHA1SignatureMethod, dsig.RSASHA512SignatureMethod} {
		t.Run(method, func(t *testing.T) {
			mustRefuse(t, v, encoded(t, f.response(f.sign(t, f.assertion(), method))), "_request_private")
		})
	}
	for name, mutate := range map[string]func(*etree.Element){
		"destination":         func(r *etree.Element) { r.CreateAttr("Destination", "https://other.iam104.test/acs") },
		"missing-destination": func(r *etree.Element) { r.RemoveAttr("Destination") },
		"duplicate-id":        func(r *etree.Element) { r.CreateAttr("ID", "_assertion_private") },
		"unsigned-assertion":  func(r *etree.Element) { a := r.SelectElement("Assertion"); a.RemoveChild(a.SelectElement("Signature")) },
		"two-valid-assertions": func(r *etree.Element) {
			a := f.assertion()
			a.CreateAttr("ID", "_second_assertion")
			r.AddChild(f.sign(t, a, rsaSHA256))
		},
		"wrapped-assertion": func(r *etree.Element) {
			a := r.SelectElement("Assertion")
			r.RemoveChild(a)
			child(r, "samlp:Extensions", "").AddChild(a)
		},
		"encrypted-identifier": func(r *etree.Element) { s := r.FindElement("./Assertion/Subject"); child(s, "saml:EncryptedID", "") },
	} {
		t.Run(name, func(t *testing.T) {
			r := f.response(f.sign(t, f.assertion(), rsaSHA256))
			mutate(r)
			r = f.sign(t, r, rsaSHA256)
			mustRefuse(t, v, encoded(t, r), "_request_private")
		})
	}
	// This modification leaves a well-formed envelope with a bad cryptographic signature.
	r := f.response(f.sign(t, f.assertion(), rsaSHA256))
	r.FindElement("./Assertion/Subject/NameID").SetText("substituted")
	mustRefuse(t, v, encoded(t, r), "_request_private")
}
func TestXMLBudgetAndNamespaceRefusals(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	valid := encoded(t, f.response(f.sign(t, f.assertion(), rsaSHA256)))
	raw, _ := base64.StdEncoding.DecodeString(valid)
	cases := []string{string(raw) + string(raw), "<!DOCTYPE Response [<!ENTITY external SYSTEM 'https://blocked.invalid/'>]>" + string(raw), strings.Replace(string(raw), "saml:Assertion", "evil:Assertion", 1), strings.Replace(string(raw), "Version=\"2.0\"", "Version=\"2.0\" Version=\"2.0\"", 1), strings.Repeat("<n>", 30) + strings.Repeat("</n>", 30), strings.Repeat(" ", f.cfg.Limits.Bytes+1), "<!--comment-->" + string(raw)}
	for _, bad := range cases {
		mustRefuse(t, v, base64.StdEncoding.EncodeToString([]byte(bad)), "_request_private")
	}
	mustRefuse(t, v, valid, "_other_request")
	mustRefuse(t, v, valid, " ")
	mustRefuse(t, v, valid+"\n", "_request_private")
}
func TestPinnedKeyRotation(t *testing.T) {
	f := newFixture(t)
	next := newFixture(t)
	oldProof := encoded(t, f.response(f.sign(t, f.assertion(), rsaSHA256)))
	newProof := encoded(t, f.response(next.sign(t, f.assertion(), rsaSHA256)))
	cfg := f.cfg
	cfg.Metadata = f.metadata(t, f.cert, next.cert)
	overlap := makeVerifier(t, cfg)
	if _, e := overlap.verify(oldProof, "_request_private"); e != nil {
		t.Fatal("old overlap key failed", e)
	}
	if _, e := overlap.verify(newProof, "_request_private"); e != nil {
		t.Fatal("new overlap key failed", e)
	}
	cfg.Metadata = f.metadata(t, next.cert)
	rotated := makeVerifier(t, cfg)
	mustRefuse(t, rotated, oldProof, "_request_private")
	if _, e := rotated.verify(newProof, "_request_private"); e != nil {
		t.Fatal(e)
	}
	cfg = f.cfg
	cfg.TrustedUntil = f.now.Add(-time.Second)
	if _, e := newVerifier(cfg); !errors.Is(e, invalidConfiguration) {
		t.Fatal("expired approval accepted")
	}
}
func TestConcurrentScopedVerifiers(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	raw := encoded(t, f.response(f.sign(t, f.assertion(), rsaSHA256)))
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := v.verify(raw, "_request_private"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	// The repeated crypto proof is deliberately not a durable one-use flow claim.
}

func TestLibraryCompatibilityRefusals(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	t.Run("signed-response-multiple-assertions", func(t *testing.T) {
		r := f.response(f.sign(t, f.assertion(), rsaSHA256))
		a := f.assertion()
		a.CreateAttr("ID", "_second_assertion")
		r.AddChild(f.sign(t, a, rsaSHA256))
		r = f.sign(t, r, rsaSHA256)
		encodedResponse := encoded(t, r)
		raw, _ := base64.StdEncoding.DecodeString(encodedResponse)
		if _, e := v.sp.ParseXMLResponse(raw, []string{"_request_private"}, v.sp.AcsURL); e != nil {
			t.Fatal("fixture did not establish library compatibility behavior")
		}
		mustRefuse(t, v, encodedResponse, "_request_private")
	})
	t.Run("unsigned-response-missing-destination", func(t *testing.T) {
		r := f.response(f.sign(t, f.assertion(), rsaSHA256))
		r.RemoveAttr("Destination")
		encodedResponse := encoded(t, r)
		raw, _ := base64.StdEncoding.DecodeString(encodedResponse)
		if _, e := v.sp.ParseXMLResponse(raw, []string{"_request_private"}, v.sp.AcsURL); e != nil {
			t.Fatal("fixture did not establish library compatibility behavior")
		}
		mustRefuse(t, v, encodedResponse, "_request_private")
	})
}

func TestSignedFixtureUpstreamInteroperability(t *testing.T) {
	f := newFixture(t)
	v := makeVerifier(t, f.cfg)
	a := f.sign(t, f.assertion(), rsaSHA256)
	encodedResponse := encoded(t, f.response(a))
	raw, _ := base64.StdEncoding.DecodeString(encodedResponse)
	if v.profile(raw, "_request_private") != nil {
		t.Fatal("positive fixture failed strict outer profile")
	}
	if _, e := v.sp.ParseXMLResponse(raw, []string{"_request_private"}, v.sp.AcsURL); e != nil {
		t.Fatal("positive fixture failed upstream signature validation")
	}
}
