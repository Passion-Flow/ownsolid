//go:build repository_integration

package sso

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
)

const (
	assertionNS   = "urn:oasis:names:tc:SAML:2.0:assertion"
	protocolNS    = "urn:oasis:names:tc:SAML:2.0:protocol"
	metadataNS    = "urn:oasis:names:tc:SAML:2.0:metadata"
	signatureNS   = "http://www.w3.org/2000/09/xmldsig#"
	exclusiveC14N = "http://www.w3.org/2001/10/xml-exc-c14n#"
	c14n11        = "http://www.w3.org/2006/12/xml-c14n11"
	enveloped     = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
	rsaSHA256     = "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
	sha256Digest  = "http://www.w3.org/2001/04/xmlenc#sha256"
)

var refused = errors.New("SAML_RESPONSE_REFUSED")
var invalidConfiguration = errors.New("SAML_CONFIGURATION_INVALID")
var xmlID = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

type limits struct{ Bytes, Depth, Nodes, Attributes int }
type node struct {
	name     xml.Name
	attrs    map[xml.Name]string
	text     strings.Builder
	children []*node
}

func (n *node) attr(name string) string       { return n.attrs[xml.Name{Local: name}] }
func (n *node) named(space, name string) bool { return n.name.Space == space && n.name.Local == name }
func (n *node) all(space, name string) []*node {
	var out []*node
	for _, v := range n.children {
		if v.named(space, name) {
			out = append(out, v)
		}
	}
	return out
}
func (n *node) one(space, name string) *node {
	v := n.all(space, name)
	if len(v) != 1 {
		return nil
	}
	return v[0]
}
func (n *node) walk(f func(*node) bool) bool {
	if !f(n) {
		return false
	}
	for _, v := range n.children {
		if !v.walk(f) {
			return false
		}
	}
	return true
}

// This guard bounds and narrows XML before the upstream DOM/signature parser.
// It makes no signature decision and supplies no identity to the caller.
func boundedXML(raw []byte, b limits) (*node, error) {
	if len(raw) == 0 || len(raw) > b.Bytes || !utf8.Valid(raw) {
		return nil, refused
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = true
	var root *node
	var stack []*node
	ids := map[string]bool{}
	nodes, tokens := 0, 0
	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, refused
		}
		tokens++
		if tokens > b.Nodes*4 {
			return nil, refused
		}
		switch v := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > b.Nodes || len(stack) >= b.Depth || len(v.Attr) > b.Attributes {
				return nil, refused
			}
			n := &node{name: v.Name, attrs: map[xml.Name]string{}}
			for _, a := range v.Attr {
				if _, ok := n.attrs[a.Name]; ok {
					return nil, refused
				}
				n.attrs[a.Name] = a.Value
				if strings.EqualFold(a.Name.Local, "id") {
					if a.Name.Space != "" || a.Name.Local != "ID" || !xmlID.MatchString(a.Value) || ids[a.Value] {
						return nil, refused
					}
					ids[a.Value] = true
				}
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, refused
				}
				root = n
			} else {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != v.Name {
				return nil, refused
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if strings.TrimSpace(string(v)) != "" {
					return nil, refused
				}
			} else {
				stack[len(stack)-1].text.Write(v)
			}
		case xml.ProcInst:
			if v.Target != "xml" || root != nil || tokens != 1 {
				return nil, refused
			}
		default:
			return nil, refused
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, refused
	}
	return root, nil
}

type configuration struct {
	SP, ACS, Issuer, Endpoint          string
	Metadata                           []byte
	TrustedUntil                       time.Time
	Clock                              *dsig.Clock
	Limits                             limits
	MaxAge, Skew, MaxAssertionLifetime time.Duration
}
type verifier struct {
	cfg configuration
	sp  *saml.ServiceProvider
}
type subject struct{ Issuer, Format, NameQualifier, SPNameQualifier, Value string }
type proof struct {
	Subject            subject
	AssertionID        string
	RequestID          string
	SessionEnd         time.Time
	AuthnAt, ExpiresAt time.Time
}
type noNetwork struct{}

func (noNetwork) RoundTrip(*http.Request) (*http.Response, error) { return nil, refused }
func approvedURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, invalidConfiguration
	}
	return u, nil
}
func entityIdentifier(raw string) bool {
	if raw == "" || len(raw) > 4096 || !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > 1024 {
		return false
	}
	for _, r := range raw {
		if r <= 32 || r == 127 {
			return false
		}
	}
	u, e := url.Parse(raw)
	return e == nil && u.IsAbs()
}
func newVerifier(c configuration) (*verifier, error) {
	if c.Clock == nil || !entityIdentifier(c.SP) || !entityIdentifier(c.Issuer) || c.MaxAge <= 0 || c.MaxAge > 90*time.Second || c.Skew < 0 || c.Skew > time.Minute || c.MaxAssertionLifetime <= 0 || c.MaxAssertionLifetime > 10*time.Minute || c.Limits.Bytes < 4096 || c.Limits.Bytes > 512<<10 || c.Limits.Depth < 8 || c.Limits.Depth > 32 || c.Limits.Nodes < 64 || c.Limits.Nodes > 4096 || c.Limits.Attributes < 8 || c.Limits.Attributes > 64 || !c.TrustedUntil.After(c.Clock.Now()) {
		return nil, invalidConfiguration
	}
	acs, e := approvedURL(c.ACS)
	if e != nil {
		return nil, e
	}
	if _, e = approvedURL(c.Endpoint); e != nil {
		return nil, e
	}
	root, e := boundedXML(c.Metadata, c.Limits)
	if e != nil || !root.named(metadataNS, "EntityDescriptor") || root.attr("entityID") != c.Issuer {
		return nil, invalidConfiguration
	}
	var meta saml.EntityDescriptor
	if e = xml.Unmarshal(c.Metadata, &meta); e != nil || len(meta.IDPSSODescriptors) != 1 {
		return nil, invalidConfiguration
	}
	descriptor := meta.IDPSSODescriptors[0]
	selected := 0
	keys := 0
	seen := map[string]bool{}
	for _, s := range descriptor.SingleSignOnServices {
		if s.Binding == saml.HTTPRedirectBinding {
			if s.Location != c.Endpoint {
				return nil, invalidConfiguration
			}
			selected++
		}
	}
	if selected != 1 {
		return nil, invalidConfiguration
	}
	for _, kd := range descriptor.KeyDescriptors {
		if kd.Use != "" && kd.Use != "signing" {
			continue
		}
		for _, v := range kd.KeyInfo.X509Data.X509Certificates {
			der, e := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(v.Data), ""))
			if e != nil {
				return nil, invalidConfiguration
			}
			cert, e := x509.ParseCertificate(der)
			if e != nil {
				return nil, invalidConfiguration
			}
			key, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.E < 65537 || !c.Clock.Now().Before(cert.NotAfter) || c.Clock.Now().Before(cert.NotBefore) || seen[string(der)] {
				return nil, invalidConfiguration
			}
			seen[string(der)] = true
			keys++
		}
	}
	if keys < 1 || keys > 4 {
		return nil, invalidConfiguration
	}
	sp := &saml.ServiceProvider{EntityID: c.SP, AcsURL: *acs, IDPMetadata: &meta, Clock: c.Clock, AuthnNameIDFormat: saml.PersistentNameIDFormat, AssertionConsumerServiceBindings: []string{saml.HTTPPostBinding}, RejectSHA1Signatures: true, RejectUnknownConditions: true, RejectExpiredIDPMetadata: true, RequireSignedResponseForCBC: true, LimitRelayStateLength: true, HTTPClient: &http.Client{Transport: noNetwork{}, Timeout: time.Second}}
	return &verifier{cfg: c, sp: sp}, nil
}
func canonical(raw string) bool { return raw == exclusiveC14N || raw == c14n11 }
func signaturePolicy(sig, parent *node) bool {
	if parent.attr("ID") == "" || len(sig.all(signatureNS, "SignedInfo")) != 1 || len(sig.all(signatureNS, "SignatureValue")) != 1 || len(sig.all(signatureNS, "KeyInfo")) > 1 {
		return false
	}
	info := sig.one(signatureNS, "SignedInfo")
	if len(info.children) != 3 {
		return false
	}
	canon, method, ref := info.children[0], info.children[1], info.children[2]
	if !canon.named(signatureNS, "CanonicalizationMethod") || !canonical(canon.attr("Algorithm")) || !method.named(signatureNS, "SignatureMethod") || method.attr("Algorithm") != rsaSHA256 || !ref.named(signatureNS, "Reference") || ref.attr("URI") != "#"+parent.attr("ID") || len(ref.children) != 3 {
		return false
	}
	transforms, digest, value := ref.children[0], ref.children[1], ref.children[2]
	if !transforms.named(signatureNS, "Transforms") || len(transforms.children) != 2 || !digest.named(signatureNS, "DigestMethod") || digest.attr("Algorithm") != sha256Digest || !value.named(signatureNS, "DigestValue") {
		return false
	}
	if !transforms.children[0].named(signatureNS, "Transform") || transforms.children[0].attr("Algorithm") != enveloped || !transforms.children[1].named(signatureNS, "Transform") || !canonical(transforms.children[1].attr("Algorithm")) {
		return false
	}
	allowed := map[string]bool{"Signature": true, "SignedInfo": true, "CanonicalizationMethod": true, "SignatureMethod": true, "Reference": true, "Transforms": true, "Transform": true, "DigestMethod": true, "DigestValue": true, "SignatureValue": true, "KeyInfo": true, "X509Data": true, "X509Certificate": true, "KeyName": true}
	return sig.walk(func(n *node) bool {
		if n.name.Space == signatureNS {
			return allowed[n.name.Local]
		}
		return n.named(exclusiveC14N, "InclusiveNamespaces")
	})
}
func (v *verifier) profile(raw []byte, request string) error {
	root, e := boundedXML(raw, v.cfg.Limits)
	if e != nil || !root.named(protocolNS, "Response") || root.attr("Destination") != v.cfg.ACS || root.attr("InResponseTo") != request || root.attr("Version") != "2.0" || root.attr("ID") == "" {
		return refused
	}
	if issuer := root.one(assertionNS, "Issuer"); issuer == nil || issuer.text.String() != v.cfg.Issuer {
		return refused
	}
	assertion := root.one(assertionNS, "Assertion")
	if assertion == nil || assertion.attr("ID") == "" || assertion.attr("Version") != "2.0" {
		return refused
	}
	if len(assertion.all(signatureNS, "Signature")) != 1 || len(root.all(signatureNS, "Signature")) > 1 {
		return refused
	}
	for _, n := range root.children {
		if !(n.named(assertionNS, "Issuer") || n.named(signatureNS, "Signature") || n.named(protocolNS, "Status") || n == assertion) {
			return refused
		}
	}
	if len(root.all(protocolNS, "Status")) != 1 {
		return refused
	}
	for _, n := range assertion.children {
		if !(n.named(assertionNS, "Issuer") || n.named(signatureNS, "Signature") || n.named(assertionNS, "Subject") || n.named(assertionNS, "Conditions") || n.named(assertionNS, "AuthnStatement") || n.named(assertionNS, "AttributeStatement")) {
			return refused
		}
	}
	for _, name := range []string{"Issuer", "Subject", "Conditions", "AuthnStatement"} {
		if len(assertion.all(assertionNS, name)) != 1 {
			return refused
		}
	}
	subjectNode := assertion.one(assertionNS, "Subject")
	if subjectNode.one(assertionNS, "NameID") == nil || subjectNode.one(assertionNS, "SubjectConfirmation") == nil || len(subjectNode.children) != 2 || len(subjectNode.one(assertionNS, "NameID").children) != 0 {
		return refused
	}
	nameNode := subjectNode.one(assertionNS, "NameID")
	for attr, value := range nameNode.attrs {
		if attr.Space == "xmlns" || (attr.Space == "" && attr.Local == "xmlns") {
			continue
		}
		if attr.Space != "" {
			return refused
		}
		switch attr.Local {
		case "Format":
			if value != string(saml.PersistentNameIDFormat) {
				return refused
			}
		case "NameQualifier":
			if value != v.cfg.Issuer {
				return refused
			}
		case "SPNameQualifier":
			if value != v.cfg.SP {
				return refused
			}
		default:
			return refused
		}
	}
	confirmationNode := subjectNode.one(assertionNS, "SubjectConfirmation")
	if len(confirmationNode.children) != 1 || confirmationNode.one(assertionNS, "SubjectConfirmationData") == nil {
		return refused
	}

	if !root.walk(func(n *node) bool {
		if n.named(assertionNS, "EncryptedAssertion") || n.named(assertionNS, "EncryptedID") || n.named(assertionNS, "EncryptedAttribute") {
			return false
		}
		if n.named(assertionNS, "Assertion") && n != assertion {
			return false
		}
		if n.named(signatureNS, "Signature") {
			parent := assertion
			if len(root.all(signatureNS, "Signature")) == 1 && n == root.one(signatureNS, "Signature") {
				parent = root
			} else if n != assertion.one(signatureNS, "Signature") {
				return false
			}
			return signaturePolicy(n, parent)
		}
		return true
	}) {
		return refused
	}
	return nil
}

func (v *verifier) verify(encoded, request string) (proof, error) {
	if !xmlID.MatchString(request) || len(encoded) > base64.StdEncoding.EncodedLen(v.cfg.Limits.Bytes) || strings.ContainsAny(encoded, "\r\n\t ") || !v.cfg.TrustedUntil.After(v.cfg.Clock.Now()) {
		return proof{}, refused
	}
	raw, e := base64.StdEncoding.Strict().DecodeString(encoded)
	if e != nil || v.profile(raw, request) != nil {
		return proof{}, refused
	}
	assertion, e := v.sp.ParseXMLResponse(raw, []string{request}, v.sp.AcsURL)
	if e != nil || assertion == nil {
		return proof{}, refused
	}
	now := v.cfg.Clock.Now()
	a := assertion
	if a.Subject == nil || a.Subject.NameID == nil || a.Conditions == nil || len(a.Subject.SubjectConfirmations) != 1 || len(a.AuthnStatements) != 1 || a.Issuer.Value != v.cfg.Issuer || a.IssueInstant.Before(now.Add(-v.cfg.MaxAge)) || a.IssueInstant.After(now.Add(v.cfg.Skew)) {
		return proof{}, refused
	}
	name := a.Subject.NameID
	if name.Format != string(saml.PersistentNameIDFormat) || name.Value == "" || len(name.Value) > 1024 || !utf8.ValidString(name.Value) || utf8.RuneCountInString(name.Value) > 256 || strings.ContainsRune(name.Value, 0) || (name.NameQualifier != "" && name.NameQualifier != v.cfg.Issuer) || (name.SPNameQualifier != "" && name.SPNameQualifier != v.cfg.SP) || name.SPProvidedID != "" {
		return proof{}, refused
	}
	conditions := a.Conditions
	confirmation := a.Subject.SubjectConfirmations[0]
	data := confirmation.SubjectConfirmationData
	if data == nil || confirmation.Method != "urn:oasis:names:tc:SAML:2.0:cm:bearer" || data.InResponseTo != request || data.Recipient != v.cfg.ACS || data.NotOnOrAfter.IsZero() || !now.Before(data.NotOnOrAfter) || conditions.NotBefore.IsZero() || conditions.NotBefore.After(now.Add(v.cfg.Skew)) || !now.Before(conditions.NotOnOrAfter) || !conditions.NotOnOrAfter.After(conditions.NotBefore) || conditions.NotOnOrAfter.Sub(conditions.NotBefore) > v.cfg.MaxAssertionLifetime {
		return proof{}, refused
	}
	expiry := conditions.NotOnOrAfter
	if v.cfg.TrustedUntil.Before(expiry) {
		expiry = v.cfg.TrustedUntil
	}
	if data.NotOnOrAfter.Before(expiry) {
		expiry = data.NotOnOrAfter
	}
	auth := a.AuthnStatements[0]
	if auth.AuthnInstant.IsZero() || auth.AuthnInstant.After(now.Add(v.cfg.Skew)) {
		return proof{}, refused
	}
	if auth.SessionNotOnOrAfter != nil && auth.SessionNotOnOrAfter.Before(expiry) {
		expiry = *auth.SessionNotOnOrAfter
	}
	if !now.Before(expiry) {
		return proof{}, refused
	}
	// The caller still owns the durable request/browser claim and the atomic
	// binding/session/audit completion. This proof never creates local authority.
	sessionEnd := time.Time{}
	if auth.SessionNotOnOrAfter != nil {
		sessionEnd = auth.SessionNotOnOrAfter.UTC()
	}
	return proof{SessionEnd: sessionEnd, RequestID: request, Subject: subject{v.cfg.Issuer, name.Format, v.cfg.Issuer, v.cfg.SP, name.Value}, AssertionID: a.ID, AuthnAt: auth.AuthnInstant, ExpiresAt: expiry}, nil
}
