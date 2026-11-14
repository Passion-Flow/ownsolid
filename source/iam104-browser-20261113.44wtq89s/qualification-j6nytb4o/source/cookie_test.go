package cookieprobe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const flowCookie = "__Host-IAM104Flow"
const sessionCookie = "__Host-IAM104Session"
const csrfCookie = "__Host-IAM104CSRF"
const continuationCookie = "__Host-IAM104Continuation"

type runtimeConfiguration struct {
	PlaywrightModule   string `json:"playwright_module"`
	Node               string `json:"node_executable"`
	ExpectedPlaywright string `json:"expected_playwright"`
	ExpectedBrowser    string `json:"expected_browser"`
	Script             string `json:"script"`
	Directory          string `json:"directory"`
}
type browserConfiguration struct {
	Module, SP, IDP, Untrusted, HostRules, Results, Credits, ExpectedPlaywright, ExpectedBrowser string
	Pins                                                                                         []string
}
type observation struct {
	Method, FetchSite, Origin                        string
	Flow, Lax, Strict, Continuation, ExpectedSession bool
	Status                                           int
}
type transportCase struct {
	session, flow, relay, continuation string
	ACS, Finish                        observation
	Finished                           bool
}
type transportProbe struct {
	mu      sync.Mutex
	cases   map[string]*transportCase
	sp, idp string
}
type certificateAuthority struct {
	certificate *x509.Certificate
	key         *ecdsa.PrivateKey
	roots       *x509.CertPool
}

func randomValue(t *testing.T) string {
	t.Helper()
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal("private fixture random value unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
func newAuthority(t *testing.T) certificateAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("private fixture CA key unavailable")
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "IAM104 Cookie Probe CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	raw, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal("private fixture CA creation failed")
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal("private fixture CA invalid")
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return certificateAuthority{parsed, key, roots}
}
func (a certificateAuthority) leaf(t *testing.T, name string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("private fixture leaf key unavailable")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal("private fixture leaf serial unavailable")
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	raw, err := x509.CreateCertificate(rand.Reader, cert, a.certificate, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal("private fixture leaf creation failed")
	}
	leaf, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal("private fixture leaf invalid")
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: a.roots, DNSName: name}); err != nil {
		t.Fatal("fixture chain, DNS or validity verification failed")
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return tls.Certificate{Certificate: [][]byte{raw, a.certificate.Raw}, PrivateKey: key}, base64.StdEncoding.EncodeToString(pin[:])
}
func cookie(r *http.Request, name string) (string, bool) {
	var value string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			value = c.Value
			count++
		}
	}
	return value, count == 1 && value != ""
}
func setCookie(w http.ResponseWriter, name, value string, site http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: site, MaxAge: 60})
}
func observe(r *http.Request) observation {
	_, flow := cookie(r, flowCookie)
	_, lax := cookie(r, sessionCookie)
	_, strict := cookie(r, csrfCookie)
	_, continuation := cookie(r, continuationCookie)
	return observation{Method: r.Method, FetchSite: r.Header.Get("Sec-Fetch-Site"), Flow: flow, Lax: lax, Strict: strict, Continuation: continuation}
}

var idpForm = template.Must(template.New("post").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Private cookie transport probe</title></head><body><h1>Private cross-site form</h1><form method="post" action="{{.Action}}"><input type="hidden" name="case" value="{{.Case}}"><input type="hidden" name="RelayState" value="{{.Relay}}"><button id="submit" type="submit">Continue transport probe</button></form></body></html>`))

func (p *transportProbe) spHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p.mu.Lock()
	defer p.mu.Unlock()
	if "https://"+r.Host != p.sp {
		http.Error(w, "invalid fixture authority", 400)
		return
	}
	switch r.URL.Path {
	case "/start":
		label := r.URL.Query().Get("case")
		c := p.cases[label]
		if r.Method != "GET" || c == nil {
			http.Error(w, "invalid fixture start", 400)
			return
		}
		setCookie(w, flowCookie, c.flow, http.SameSiteNoneMode)
		setCookie(w, sessionCookie, c.session, http.SameSiteLaxMode)
		setCookie(w, csrfCookie, c.relay, http.SameSiteStrictMode)
		http.Redirect(w, r, p.idp+"/form?case="+url.QueryEscape(label), 302)
	case "/acs":
		if r.Method != "POST" {
			http.Error(w, "invalid fixture POST", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil || len(r.PostForm) != 2 || len(r.PostForm["case"]) != 1 || len(r.PostForm["RelayState"]) != 1 {
			http.Error(w, "invalid fixture form", 400)
			return
		}
		c := p.cases[r.PostForm.Get("case")]
		if c == nil {
			http.Error(w, "invalid fixture case", 400)
			return
		}
		c.ACS = observe(r)
		switch r.Header.Get("Origin") {
		case p.idp:
			c.ACS.Origin = "expected-idp"
		case "null":
			c.ACS.Origin = "null"
		case "":
			c.ACS.Origin = "absent"
		default:
			c.ACS.Origin = "other"
		}
		if c.ACS.Origin != "expected-idp" {
			c.ACS.Status = 403
			http.Error(w, "invalid fixture POST origin", 403)
			return
		}
		value, ok := cookie(r, flowCookie)
		if !ok || value != c.flow || r.PostForm.Get("RelayState") != c.relay {
			c.ACS.Status = 403
			http.Error(w, "fixture browser proof missing", 403)
			return
		}
		c.ACS.Status = 303
		setCookie(w, continuationCookie, c.continuation, http.SameSiteLaxMode)
		// This probe stages only browser transport. It verifies no SAML message and
		// grants no account binding, authentication, role or local session.
		http.Redirect(w, r, p.sp+"/finish", 303)
	case "/finish":
		value, ok := cookie(r, continuationCookie)
		var c *transportCase
		for _, candidate := range p.cases {
			if ok && candidate.continuation == value {
				c = candidate
				break
			}
		}
		if r.Method != "GET" || c == nil {
			http.Error(w, "fixture continuation missing", 403)
			return
		}
		c.Finish = observe(r)
		session, present := cookie(r, sessionCookie)
		c.Finish.ExpectedSession = present && session == c.session
		if !c.Finish.ExpectedSession {
			c.Finish.Status = 403
			http.Error(w, "fixture local browser changed", 403)
			return
		}
		c.Finish.Status = 200
		c.Finished = true
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><title>Private transport complete</title><h1>Transport complete</h1>")
	default:
		http.NotFound(w, r)
	}
}
func (p *transportProbe) idpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Method != "GET" || "https://"+r.Host != p.idp || r.URL.Path != "/form" {
		http.Error(w, "invalid fixture form authority", 400)
		return
	}
	label := r.URL.Query().Get("case")
	c := p.cases[label]
	if c == nil {
		http.Error(w, "invalid fixture case", 400)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if idpForm.Execute(w, map[string]string{"Action": p.sp + "/acs", "Case": label, "Relay": c.relay}) != nil {
		return
	}
}
func startServer(t *testing.T, pair tls.Certificate, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ReadHeaderTimeout = 5 * time.Second
	server.Config.ReadTimeout = 5 * time.Second
	server.Config.WriteTimeout = 5 * time.Second
	server.Config.MaxHeaderBytes = 16384
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal("owned fixture listener invalid")
	}
	return server, port
}

func TestActualCrossSiteCookieAndFirstPartyContinuation(t *testing.T) {
	runtimeFile := os.Getenv("IAM_SAML_BROWSER_RUNTIME_FILE")
	if runtimeFile == "" {
		t.Skip("explicit private browser runtime required")
	}
	raw, err := os.ReadFile(runtimeFile)
	if err != nil {
		t.Fatal("private browser runtime unavailable")
	}
	var runtime runtimeConfiguration
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&runtime) != nil || runtime.Node == "" || runtime.Script == "" || runtime.Directory == "" {
		t.Fatal("private browser runtime invalid")
	}
	p := &transportProbe{cases: map[string]*transportCase{}}
	for _, label := range []string{"positive", "missing_flow", "swapped_session"} {
		p.cases[label] = &transportCase{session: randomValue(t), flow: randomValue(t), relay: randomValue(t), continuation: randomValue(t)}
	}
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		observations := map[string]any{}
		for label, c := range p.cases {
			observations[label] = map[string]any{"acs": c.ACS, "finish": c.Finish}
		}
		raw, err := json.MarshalIndent(observations, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(runtime.Directory, "all-observations.json"), raw, 0600) != nil {
			t.Error("private fixture observations unavailable")
		}
	}()
	authority := newAuthority(t)
	spPair, spPin := authority.leaf(t, "portal.iam104.test")
	idpPair, idpPin := authority.leaf(t, "login.idp104.test")
	untrustedPair, _ := newAuthority(t).leaf(t, "untrusted.probe104.test")
	_, spPort := startServer(t, spPair, http.HandlerFunc(p.spHandler))
	_, idpPort := startServer(t, idpPair, http.HandlerFunc(p.idpHandler))
	_, untrustedPort := startServer(t, untrustedPair, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unpinned fixture reached", 500) }))
	p.mu.Lock()
	p.sp = "https://portal.iam104.test:" + spPort
	p.idp = "https://login.idp104.test:" + idpPort
	p.mu.Unlock()
	config := browserConfiguration{Module: runtime.PlaywrightModule, SP: p.sp, IDP: p.idp, Untrusted: "https://untrusted.probe104.test:" + untrustedPort, HostRules: "MAP portal.iam104.test 127.0.0.1,MAP login.idp104.test 127.0.0.1,MAP untrusted.probe104.test 127.0.0.1", Pins: []string{spPin, idpPin}, Results: filepath.Join(runtime.Directory, "browser-results.json"), Credits: filepath.Join(runtime.Directory, "browser-credits.html"), ExpectedPlaywright: runtime.ExpectedPlaywright, ExpectedBrowser: runtime.ExpectedBrowser}
	configFile := filepath.Join(runtime.Directory, "browser-config.json")
	encoded, err := json.Marshal(config)
	if err != nil || os.WriteFile(configFile, encoded, 0600) != nil {
		t.Fatal("private browser invocation unavailable")
	}
	logFile, err := os.OpenFile(filepath.Join(runtime.Directory, "browser.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("private browser log unavailable")
	}
	defer logFile.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, runtime.Node, runtime.Script, configFile)
	command.Stdout, command.Stderr = logFile, logFile
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 5 * time.Second
	if command.Run() != nil {
		t.Fatal("actual private browser probe failed; inspect retained private log")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	positive, missing, swapped := p.cases["positive"], p.cases["missing_flow"], p.cases["swapped_session"]
	for _, c := range []*transportCase{positive, missing, swapped} {
		if c.ACS.Method != "POST" || c.ACS.FetchSite != "cross-site" || c.ACS.Lax || c.ACS.Strict {
			t.Fatal("actual POST did not demonstrate cross-site cookie isolation")
		}
	}
	if !positive.ACS.Flow || positive.ACS.Status != 303 || positive.Finish.Method != "GET" || !positive.Finish.Lax || !positive.Finish.Continuation || !positive.Finish.ExpectedSession || positive.Finish.Status != 200 || !positive.Finished {
		t.Fatal("first-party continuation did not recover the initiating Lax cookie")
	}
	if missing.ACS.Flow || missing.ACS.Status != 403 || missing.Finish.Status != 0 || missing.Finished {
		t.Fatal("missing flow cookie reached continuation")
	}
	if !swapped.ACS.Flow || swapped.ACS.Status != 303 || !swapped.Finish.Lax || swapped.Finish.ExpectedSession || swapped.Finish.Status != 403 || swapped.Finished {
		t.Fatal("changed local browser cookie was not refused")
	}
	result := map[string]any{"positive": map[string]any{"acs": positive.ACS, "finish": positive.Finish}, "missing_flow": map[string]any{"acs": missing.ACS, "finish": missing.Finish}, "swapped_session": map[string]any{"acs": swapped.ACS, "finish": swapped.Finish}, "authentication_grants_created": 0, "native_mac_not_egress_isolated": true}
	encoded, err = json.MarshalIndent(result, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(runtime.Directory, "server-observations.json"), encoded, 0600) != nil {
		t.Fatal("private browser observations unavailable")
	}
}
