package privateclusters

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type apiError struct {
	code    string
	unknown bool
}

func (e *apiError) Error() string                 { return e.code }
func remoteError(code string, unknown bool) error { return &apiError{code, unknown} }

type client struct {
	profile                  Profile
	policy                   Policy
	http                     *http.Client
	transport                *http.Transport
	configSHA, credentialSHA string
	approval                 bundle.ImageProofResult
}

func newClient(ctx context.Context, c Config, p Profile) (*client, error) {
	if ctx == nil || ctx.Err() != nil || p.Validate() != nil || c.Policy.Validate() != nil {
		return nil, fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	proofRaw, e := bundle.ReadProtectedConfiguration(p.ImageProofFile)
	if e != nil {
		return nil, fail("PRIVATE_CLUSTER_APPROVED_IMAGE_REQUIRED")
	}
	var proof bundle.ImageProof
	if decodeProtectedJSON(proofRaw, &proof) != nil || proof.AssetID != p.AssetID() {
		return nil, fail("PRIVATE_CLUSTER_APPROVED_IMAGE_REQUIRED")
	}
	approved, e := bundle.VerifyImageProof(ctx, c.Verification, proof)
	if e != nil {
		return nil, fail("PRIVATE_CLUSTER_IMAGE_PROOF_REFUSED")
	}
	var manifest release.Manifest
	if release.StrictJSON(proof.Manifest, &manifest) != nil || approved.Asset.Architecture != p.Architecture() || approved.Asset.ID != p.AssetID() {
		return nil, fail("PRIVATE_CLUSTER_IMAGE_PROFILE_MISMATCH")
	}
	capability := false
	for _, v := range manifest.Capabilities {
		if v == "M28" {
			capability = true
		}
	}
	if !capability {
		return nil, fail("PRIVATE_CLUSTER_IMAGE_CAPABILITY_REQUIRED")
	}
	remote, e := credentialClient(ctx, c, p)
	if e != nil {
		return nil, e
	}
	remote.approval = approved
	remote.configSHA = fingerprint(struct {
		Profile         Profile
		Policy          Policy
		Manifest, Asset string
	}{p, c.Policy, approved.ManifestSHA256, approved.Asset.ImageConfigDigest})
	return remote, nil
}

// credentialClient owns the same protected credential and pinned TLS boundary
// used after manufacturer approval. It neither verifies nor grants image authority.
func credentialClient(ctx context.Context, c Config, p Profile) (*client, error) {
	if ctx == nil || ctx.Err() != nil || p.Validate() != nil || c.Policy.Validate() != nil {
		return nil, fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	ca, e := bundle.ReadProtectedConfiguration(p.CAFile)
	if e != nil {
		return nil, fail("PRIVATE_CLUSTER_CA_REQUIRED")
	}
	token, e := bundle.ReadProtectedConfiguration(p.TokenFile)
	if e != nil {
		return nil, fail("PRIVATE_CLUSTER_TOKEN_REQUIRED")
	}
	token = bytes.TrimSuffix(token, []byte{'\n'})
	if len(token) < 16 || len(token) > 8192 || !regexp.MustCompile(`^[A-Za-z0-9._~+/=\-]+$`).Match(token) {
		return nil, fail("PRIVATE_CLUSTER_TOKEN_INVALID")
	}
	roots := x509.NewCertPool()
	remaining := ca
	count := 0
	now := time.Now()
	for len(bytes.TrimSpace(remaining)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(remaining), []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, fail("PRIVATE_CLUSTER_CA_INVALID")
		}
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fail("PRIVATE_CLUSTER_CA_INVALID")
		}
		cert, e := x509.ParseCertificate(block.Bytes)
		if e != nil || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) || count >= 16 {
			return nil, fail("PRIVATE_CLUSTER_CA_INVALID")
		}
		roots.AddCert(cert)
		remaining = rest
		count++
	}
	if count == 0 {
		return nil, fail("PRIVATE_CLUSTER_CA_INVALID")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: p.TLSServerName}, DisableCompression: true, MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, ResponseHeaderTimeout: time.Duration(c.Policy.RequestSeconds) * time.Second}
	dialer := &net.Dialer{Timeout: time.Duration(c.Policy.RequestSeconds) * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, fail("PRIVATE_CLUSTER_DIAL_REFUSED")
		}
		origin, _ := url.Parse(p.ServerOrigin)
		expectedPort := origin.Port()
		if expectedPort == "" {
			expectedPort = "443"
		}
		if host != origin.Hostname() || port != expectedPort {
			return nil, fail("PRIVATE_CLUSTER_DIAL_REFUSED")
		}
		addresses, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(addresses) == 0 {
			return nil, fail("PRIVATE_CLUSTER_DIAL_REFUSED")
		}
		for _, ip := range addresses {
			allowed := false
			for _, raw := range p.AllowedCIDRs {
				prefix, _ := netip.ParsePrefix(raw)
				if prefix.Contains(ip.Unmap()) {
					allowed = true
				}
			}
			if !allowed {
				return nil, fail("PRIVATE_CLUSTER_DIAL_REFUSED")
			}
		}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(addresses[0].String(), port))
	}
	httpClient := &http.Client{Transport: bearerTransport{transport, string(token)}, Timeout: time.Duration(c.Policy.RequestSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	combined := append(append(append([]byte{}, token...), 0), ca...)
	return &client{profile: p, policy: c.Policy, http: httpClient, transport: transport, credentialSHA: release.Digest(combined)}, nil
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
func (c *client) close() { c.transport.CloseIdleConnections() }
func (c *client) raw(ctx context.Context, method, path string, body []byte, logs bool) ([]byte, error) {
	effect := method != http.MethodGet
	if ctx == nil || ctx.Err() != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\x00\r\n\\#") || int64(len(body)) > c.policy.MaximumBodyBytes {
		return nil, fail("PRIVATE_CLUSTER_REQUEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.policy.RequestSeconds)*time.Second)
	defer cancel()
	r, e := http.NewRequestWithContext(ctx, method, c.profile.ServerOrigin+path, bytes.NewReader(body))
	if e != nil {
		return nil, fail("PRIVATE_CLUSTER_REQUEST_INVALID")
	}
	r.Header.Set("Accept", "application/json")
	if logs {
		r.Header.Set("Accept", "text/plain")
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("User-Agent", "projectone-private-cluster/v1")
	response, e := c.http.Do(r)
	if e != nil {
		if ctx.Err() != nil {
			return nil, remoteError("PRIVATE_CLUSTER_REQUEST_INTERRUPTED", effect)
		}
		return nil, remoteError("PRIVATE_CLUSTER_TRANSPORT_OR_TLS_FAILED", effect)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
	case http.StatusNotFound:
		return nil, remoteError("PRIVATE_CLUSTER_RESOURCE_MISSING", false)
	case http.StatusUnauthorized:
		return nil, remoteError("PRIVATE_CLUSTER_AUTHENTICATION_REFUSED", false)
	case http.StatusForbidden:
		return nil, remoteError("PRIVATE_CLUSTER_ACCESS_FORBIDDEN", false)
	case http.StatusConflict:
		return nil, remoteError("PRIVATE_CLUSTER_RESOURCE_CONFLICT", false)
	case 301, 302, 303, 307, 308:
		// Refusing the redirect prevents a second request; it does not prove
		// that the original write had no upstream effect.
		return nil, remoteError("PRIVATE_CLUSTER_REDIRECT_REFUSED", effect)
	default:
		return nil, remoteError("PRIVATE_CLUSTER_API_STATUS_REFUSED", effect && response.StatusCode >= 500)
	}
	maximum := c.policy.MaximumBodyBytes
	if logs {
		maximum = c.policy.MaximumLogBytes
	}
	if response.ContentLength > maximum {
		return nil, remoteError("PRIVATE_CLUSTER_RESPONSE_LIMIT", effect)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if e != nil || int64(len(data)) > maximum {
		return nil, remoteError("PRIVATE_CLUSTER_RESPONSE_LIMIT_OR_READ_FAILED", effect)
	}
	if !logs {
		kind, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if e != nil || kind != "application/json" {
			return nil, remoteError("PRIVATE_CLUSTER_RESPONSE_INVALID", effect)
		}
	}
	return data, nil
}
func (c *client) json(ctx context.Context, method, path string, body, destination any) error {
	var raw []byte
	var e error
	if body != nil {
		raw, e = json.Marshal(body)
		if e != nil {
			return fail("PRIVATE_CLUSTER_REQUEST_INVALID")
		}
	}
	data, e := c.raw(ctx, method, path, raw, false)
	if e != nil {
		return e
	}
	if decodeResponse(data, destination) != nil {
		return remoteError("PRIVATE_CLUSTER_RESPONSE_INVALID", method != http.MethodGet)
	}
	return nil
}
func (c *client) get(ctx context.Context, path string, destination any) error {
	return c.json(ctx, http.MethodGet, path, nil, destination)
}
func (c *client) namespaced(resource string) string {
	return "/api/v1/namespaces/" + url.PathEscape(c.profile.Namespace) + "/" + resource
}
func (c *client) jobs() string {
	return "/apis/batch/v1/namespaces/" + url.PathEscape(c.profile.Namespace) + "/jobs"
}
func (c *client) leases() string {
	return "/apis/coordination.k8s.io/v1/namespaces/" + url.PathEscape(c.profile.Namespace) + "/leases"
}
