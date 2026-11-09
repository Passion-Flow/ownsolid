package distribution_test

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestModelGatewayCAForPublishedAliasOverTLS(t *testing.T) {
	c, _ := deliveredScenario(t)
	ctx := t.Context()
	provisioned, err := c.s.Provision(ctx, c.a, c.deliveredInput(), uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	g := provisioned.Gateway
	gateway := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(gateway.Close)
	origin := "https://" + gateway.Listener.Addr().String()
	key, err := agentwire.NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := agentwire.NewCertificateRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	lease := c.claim(t)
	activation := agentwire.GatewayActivation{
		LeaseToken: lease.LeaseToken, Credential: gatewayCredential(t, g.ID), CertificateRequest: csr, Origin: origin,
	}
	cleanBundle := []byte(c.s.Certificates.Bundle())
	material := c.s.Identity.Config.ModelGatewayIdentity
	caKey, err := os.ReadFile(material.CAPrivateKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	mixedBundle := bytes.Join([][]byte{cleanBundle, caKey}, nil)
	setBundle := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(material.CACertificateFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	setBundle(mixedBundle)
	mixedHandler := identityhttp.New(c.s.Identity)
	_, err = mixedHandler.Nodes.Service.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, activation, uuid.NewString())
	check(t, err, "GATEWAY_AUTHORITY_UNAVAILABLE")
	if before := gatewayByID(t, c, g.ID); before.ClientOrigin != nil || before.CertificateNotAfter != nil {
		t.Fatal("invalid CA left an activated gateway")
	}
	setBundle(cleanBundle)
	activated, err := c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, activation, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair([]byte(activated.Certificate), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	gateway.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	gateway.StartTLS()
	if state, err := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID,
		agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "success",
			Gateway: &agentwire.GatewayObservation{GatewayID: g.ID, State: "running", ContainerID: strings.Repeat("c", 64)}}, uuid.NewString()); err != nil || state != "succeeded" {
		t.Fatal("gateway completion failed", state, err)
	}

	deployment := c.readyDeployment(t)
	alias, err := c.s.Publish(ctx, c.a, distribution.PublishRequest{
		WorkspaceID: g.WorkspaceID, GatewayID: g.ID, DeploymentID: deployment.ID,
		DeploymentRevision: deployment.Revision, Name: "signed-model", Reason: "Synthetic trust test",
	}, uuid.NewString(), uuid.NewString())
	if err != nil || alias.ClientOrigin != origin {
		t.Fatal("publication lost the declared origin", err)
	}

	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	portal := gatewayCADownload{
		server: server, origin: httpfixture.PortalOrigin, cookie: gatewayCASession(t, c, "portal"),
	}
	admin := gatewayCADownload{
		server: server, origin: httpfixture.AdminOrigin, cookie: gatewayCASession(t, c, "admin"),
	}
	portalPath := "/api/v1/workspaces/" + g.WorkspaceID + "/distribution/aliases/" + alias.ID + "/ca"
	adminPath := "/api/v1/admin/distribution/workspaces/" + g.WorkspaceID + "/aliases/" + alias.ID + "/ca"
	bundle := portal.get(t, portalPath, http.StatusOK, "")
	if !bytes.Equal(bundle, []byte(activated.CertificateAuthority)) ||
		!bytes.Equal(bundle, admin.get(t, adminPath, http.StatusOK, "")) ||
		!bytes.Equal(bundle, portal.get(t, portalPath, http.StatusOK, "")) {
		t.Fatal("downloads did not retain the activated gateway's trust")
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		t.Fatal("downloaded bundle did not contain a CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("downloaded bundle did not verify the advertised HTTPS endpoint", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("unexpected signed endpoint response", response.StatusCode)
	}
	wrong := transport.Clone()
	wrong.TLSClientConfig.ServerName = "other.gateway.invalid"
	defer wrong.CloseIdleConnections()
	if response, err = (&http.Client{Transport: wrong}).Do(request.Clone(ctx)); err == nil {
		response.Body.Close()
		t.Fatal("downloaded bundle accepted a wrong gateway name")
	}

	anonymous := portal
	anonymous.cookie = nil
	anonymous.get(t, portalPath, http.StatusUnauthorized, "AUTH_REQUIRED")
	portal.get(t, portalPath+"?origin=https%3A%2F%2Fother.invalid", http.StatusBadRequest, "VALIDATION_FAILED")
	foreign := setup(t)
	foreignAdmin := gatewayCADownload{
		server: httpfixture.NewTLSServer(t, identityhttp.New(foreign.s.Identity)),
		origin: httpfixture.AdminOrigin, cookie: gatewayCASession(t, foreign, "admin"),
	}
	foreignAdmin.get(t, adminPath, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	manual := foreign.access(t).alias
	manualPath := "/api/v1/admin/distribution/workspaces/" + manual.WorkspaceID + "/aliases/" + manual.ID + "/ca"
	foreignAdmin.get(t, manualPath, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	// A process that loads a mixed PEM must never return the key as public trust.
	mixed := portal
	mixed.server = httpfixture.NewTLSServer(t, mixedHandler)
	mixed.get(t, portalPath, http.StatusServiceUnavailable, "GATEWAY_AUTHORITY_UNAVAILABLE")
	if !bytes.Equal(bundle, portal.get(t, portalPath, http.StatusOK, "")) {
		t.Fatal("the active CA download changed after an on-disk edit")
	}
	nextCA, nextKey := testGatewayCAMaterial(t, "replacement model gateway CA")
	overlap := bytes.Join([][]byte{nextCA, cleanBundle}, nil)
	setBundle(overlap)
	if err := os.WriteFile(material.CAPrivateKeyFile, nextKey, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	rolloverAdmin, rolloverPortal := admin, portal
	rolloverAdmin.server, rolloverPortal.server = reloaded, reloaded
	downloaded := rolloverPortal.get(t, portalPath, http.StatusOK, "")
	if !bytes.Equal(downloaded, overlap) || !bytes.Equal(downloaded, rolloverAdmin.get(t, adminPath, http.StatusOK, "")) {
		t.Fatal("issuer overlap did not return both public CAs")
	}
	if !bytes.Equal(portal.get(t, portalPath, http.StatusOK, ""), bundle) {
		t.Fatal("the original dispatcher replaced its loaded issuer")
	}
	overlapRoots := x509.NewCertPool()
	if !overlapRoots.AppendCertsFromPEM(downloaded) {
		t.Fatal("issuer overlap contained no trust anchors")
	}
	overlapTransport := transport.Clone()
	defer overlapTransport.CloseIdleConnections()
	overlapTransport.TLSClientConfig.RootCAs = overlapRoots
	originalPeer, err := (&http.Client{Transport: overlapTransport}).Do(request.Clone(ctx))
	if err != nil {
		t.Fatal("the replacement download could not verify the old serving leaf", err)
	}
	originalPeer.Body.Close()
	if originalPeer.StatusCode != http.StatusNoContent {
		t.Fatal("unexpected old gateway response during issuer overlap", originalPeer.StatusCode)
	}

	if _, err := c.s.RevokeGateway(ctx, c.a, g.WorkspaceID, g.ID, "Retire synthetic gateway", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	portal.get(t, portalPath, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

type gatewayCADownload struct {
	server *httptest.Server
	origin string
	cookie *http.Cookie
}

func gatewayCASession(t *testing.T, c scenario, audience string) *http.Cookie {
	t.Helper()
	cookies, err := httpserver.NewCookiePolicy(c.s.Identity.Config.Server.EffectiveTransportMode())
	if err != nil {
		t.Fatal(err)
	}
	_, session, _, err := c.s.Identity.Login(t.Context(), "admin@example.invalid", c.password, audience, "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: cookies.Name(audience, httpserver.SessionCookie), Value: session}
}

func (c gatewayCADownload) get(t *testing.T, path string, status int, code string) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, c.origin+cellroute.Public(path, agentwire.LocalScope()), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", c.origin)
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	response, err := c.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatal("unexpected model gateway CA response", response.StatusCode, status, err)
	}
	if status != http.StatusOK {
		var problem struct{ Code string }
		if response.Header.Get("Content-Type") != "application/problem+json" ||
			response.Header.Get("Content-Disposition") != "" || response.Header.Get("X-Artifact-SHA256") != "" ||
			json.Unmarshal(raw, &problem) != nil || problem.Code != code || bytes.Contains(raw, []byte("-----BEGIN ")) {
			t.Fatal("invalid model gateway CA problem", problem.Code, code)
		}
		return nil
	}
	disposition, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	digest := sha256.Sum256(raw)
	if err != nil || disposition != "attachment" || params["filename"] != "model-gateway-ca.pem" ||
		response.Header.Get("Content-Type") != "application/octet-stream" || response.ContentLength != int64(len(raw)) ||
		response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(digest[:]) ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("invalid model gateway CA attachment metadata")
	}
	return raw
}
