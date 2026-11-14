package imageregistry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const ownedTag = "retained"
const ociManifest = "application/vnd.oci.image.manifest.v1+json"
const dockerManifest = "application/vnd.docker.distribution.manifest.v2+json"

type registryCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type distributionClient struct {
	origin     *url.URL
	http       *http.Client
	credential registryCredential
	maximum    int64
	identity   string
}
type Descriptor struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
}

func validDigest(v string) bool {
	if len(v) != 71 || !strings.HasPrefix(v, "sha256:") {
		return false
	}
	for _, r := range v[7:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func digestBytes(v []byte) string { h := sha256.Sum256(v); return "sha256:" + hex.EncodeToString(h[:]) }
func newDistribution(c Config) (*distributionClient, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	raw, e := bundle.ReadProtectedConfiguration(c.CredentialFile)
	if e != nil {
		return nil, failure("REGISTRY_CREDENTIAL_INVALID")
	}
	var credential registryCredential
	if release.StrictJSON(raw, &credential) != nil || credential.Username == "" || credential.Password == "" || strings.ContainsAny(credential.Username, ":\r\n") || strings.ContainsAny(credential.Password, "\r\n") {
		return nil, failure("REGISTRY_CREDENTIAL_INVALID")
	}
	// Credentials are private local input, never an auth challenge redirect target.
	var unknown map[string]json.RawMessage
	if json.Unmarshal(raw, &unknown) != nil || len(unknown) != 2 {
		return nil, failure("REGISTRY_CREDENTIAL_INVALID")
	}
	pem, e := bundle.ReadProtectedConfiguration(c.CAFile)
	if e != nil {
		return nil, failure("REGISTRY_TLS_INVALID")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, failure("REGISTRY_TLS_INVALID")
	}
	origin, _ := url.Parse(strings.TrimSuffix(c.Endpoint, "/"))
	prefixes := make([]netip.Prefix, 0, len(c.AllowedCIDRs))
	for _, v := range c.AllowedCIDRs {
		p, _ := netip.ParsePrefix(v)
		prefixes = append(prefixes, p)
	}
	dialer := net.Dialer{Timeout: time.Duration(c.RequestSeconds) * time.Second}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableCompression: true, ResponseHeaderTimeout: time.Duration(c.RequestSeconds) * time.Second, MaxIdleConnsPerHost: c.MaximumConcurrentImports}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, origin.Hostname()) {
			return nil, failure("REGISTRY_ADDRESS_REJECTED")
		}
		addresses, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil {
			return nil, failure("REGISTRY_ADDRESS_REJECTED")
		}
		// Validate the complete answer before selecting a pinned address. Never redial a hostname.
		if len(addresses) == 0 {
			return nil, failure("REGISTRY_ADDRESS_REJECTED")
		}
		for _, a := range addresses {
			allowed := false
			for _, p := range prefixes {
				if p.Contains(a.Unmap()) {
					allowed = true
				}
			}
			if !allowed {
				return nil, failure("REGISTRY_ADDRESS_REJECTED")
			}
		}
		var last error
		for _, a := range addresses {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}
	identity := digestBytes(append([]byte(origin.String()+"\n"), pem...))
	return &distributionClient{origin: origin, http: &http.Client{Transport: transport, Timeout: time.Duration(c.RequestSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, credential: credential, maximum: c.MaximumBlobBytes, identity: identity}, nil
}
func repository(tenant, workspace, pin string) string {
	return "projectone/" + tenant + "/" + workspace + "/" + pin
}
func (c *distributionClient) endpoint(repo, kind, value string) (string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 4 || parts[0] != "projectone" || !validID(parts[1]) || !validID(parts[2]) || !validID(parts[3]) {
		return "", failure("REGISTRY_PATH_REJECTED")
	}
	if kind != "blobs" && kind != "manifests" {
		return "", failure("REGISTRY_PATH_REJECTED")
	}
	if !validDigest(value) && !(kind == "manifests" && value == ownedTag) {
		return "", failure("REGISTRY_PATH_REJECTED")
	}
	return c.origin.String() + "/v2/" + repo + "/" + kind + "/" + value, nil
}
func (c *distributionClient) request(ctx context.Context, method, target, media string, body io.Reader, length int64) (*http.Response, error) {
	u, e := url.Parse(target)
	if e != nil || u.Scheme != c.origin.Scheme || !strings.EqualFold(u.Host, c.origin.Host) || u.User != nil || u.Fragment != "" {
		return nil, failure("REGISTRY_PATH_REJECTED")
	}
	req, e := http.NewRequestWithContext(ctx, method, target, body)
	if e != nil {
		return nil, failure("REGISTRY_REMOTE_INVALID")
	}
	req.ContentLength = length
	req.SetBasicAuth(c.credential.Username, c.credential.Password)
	if media != "" {
		req.Header.Set("Content-Type", media)
	}
	req.Header.Set("Accept", ociManifest+", "+dockerManifest)
	r, e := c.http.Do(req)
	if e != nil {
		if method == http.MethodGet || method == http.MethodHead {
			return nil, failure("REGISTRY_UNAVAILABLE")
		}
		return nil, failure("REGISTRY_REMOTE_OUTCOME_UNKNOWN")
	}
	if r.StatusCode == http.StatusUnauthorized || r.StatusCode == http.StatusForbidden {
		r.Body.Close()
		return nil, failure("REGISTRY_AUTH_REJECTED")
	}
	if r.StatusCode >= 300 && r.StatusCode < 400 {
		r.Body.Close()
		return nil, failure("REGISTRY_REDIRECT_REJECTED")
	}
	return r, nil
}
func (c *distributionClient) verify(ctx context.Context, repo string, d Descriptor, tag bool) error {
	target, e := c.endpoint(repo, "blobs", d.Digest)
	if d.Kind == "manifest" {
		reference := d.Digest
		if tag {
			reference = ownedTag
		}
		target, e = c.endpoint(repo, "manifests", reference)
	}
	if e != nil {
		return e
	}
	return c.copyVerified(ctx, target, d, io.Discard)
}

// copyVerified streams one exact response through the digest verifier. The
// caller owns any incomplete destination and decides whether to publish it.
func (c *distributionClient) copyVerified(ctx context.Context, target string, d Descriptor, w io.Writer) (result error) {
	if !validDigest(d.Digest) || d.Size < 1 || d.Size > c.maximum {
		return failure("REGISTRY_DESCRIPTOR_INVALID")
	}
	r, e := c.request(ctx, http.MethodGet, target, "", nil, 0)
	if e != nil {
		return e
	}
	defer func() {
		if e := r.Body.Close(); e != nil && result == nil {
			result = failure("REGISTRY_REMOTE_INVALID")
		}
	}()
	if r.StatusCode == http.StatusNotFound {
		return failure("REGISTRY_CONTENT_MISSING")
	}
	if r.StatusCode != http.StatusOK || r.ContentLength != d.Size {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	if v := r.Header.Get("Docker-Content-Digest"); v != "" && v != d.Digest {
		return failure("REGISTRY_DIGEST_MISMATCH")
	}
	if d.Kind == "manifest" && strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != d.MediaType {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(w, h), io.LimitReader(r.Body, d.Size+1))
	if e != nil || n != d.Size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != d.Digest {
		return failure("REGISTRY_DIGEST_MISMATCH")
	}
	return nil
}
func (c *distributionClient) PutBlob(ctx context.Context, repo string, d Descriptor, filename string) error {
	if !validDigest(d.Digest) || d.Kind == "manifest" || d.Size < 1 || d.Size > c.maximum {
		return failure("REGISTRY_DESCRIPTOR_INVALID")
	}
	target, e := c.endpoint(repo, "blobs", d.Digest)
	if e != nil {
		return e
	}
	r, e := c.request(ctx, http.MethodHead, target, "", nil, 0)
	if e != nil {
		return e
	}
	r.Body.Close()
	if r.StatusCode == http.StatusOK {
		return c.Verify(ctx, repo, d)
	}
	if r.StatusCode != http.StatusNotFound {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	start := c.origin.String() + "/v2/" + repo + "/blobs/uploads/"
	r, e = c.request(ctx, http.MethodPost, start, "application/octet-stream", nil, 0)
	if e != nil {
		return e
	}
	r.Body.Close()
	if r.StatusCode != http.StatusAccepted {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	location, e := r.Location()
	if e != nil {
		return failure("REGISTRY_UPLOAD_LOCATION_REJECTED")
	}
	prefix := "/v2/" + repo + "/blobs/uploads/"
	opaque := strings.TrimPrefix(location.Path, prefix)
	if location.Scheme != c.origin.Scheme || !strings.EqualFold(location.Host, c.origin.Host) || location.User != nil || location.Fragment != "" || !strings.HasPrefix(location.Path, prefix) || opaque == "" || strings.ContainsAny(opaque, "/\\%") || strings.Contains(location.RawPath, "%") {
		return failure("REGISTRY_UPLOAD_LOCATION_REJECTED")
	}
	query, e := url.ParseQuery(location.RawQuery)
	if e != nil {
		return failure("REGISTRY_UPLOAD_LOCATION_REJECTED")
	}
	for key, values := range query {
		if key != "_state" || len(values) != 1 || len(values[0]) > 16384 {
			return failure("REGISTRY_UPLOAD_LOCATION_REJECTED")
		}
	}
	query.Set("digest", d.Digest)
	location.RawQuery = query.Encode()
	f, e := os.Open(filename)
	if e != nil {
		return failure("REGISTRY_CONTENT_MISSING")
	}
	defer f.Close()
	r, e = c.request(ctx, http.MethodPut, location.String(), "application/octet-stream", io.LimitReader(f, d.Size), d.Size)
	if e != nil {
		return e
	}
	r.Body.Close()
	if r.StatusCode != http.StatusCreated {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	return c.Verify(ctx, repo, d)
}
func (c *distributionClient) PutManifest(ctx context.Context, repo string, d Descriptor, raw []byte) error {
	if d.Kind != "manifest" || d.Size != int64(len(raw)) || digestBytes(raw) != d.Digest || d.MediaType != ociManifest && d.MediaType != dockerManifest {
		return failure("REGISTRY_DESCRIPTOR_INVALID")
	}
	for _, reference := range []string{d.Digest, ownedTag} {
		target, e := c.endpoint(repo, "manifests", reference)
		if e != nil {
			return e
		}
		r, e := c.request(ctx, http.MethodPut, target, d.MediaType, strings.NewReader(string(raw)), d.Size)
		if e != nil {
			return e
		}
		r.Body.Close()
		if r.StatusCode != http.StatusCreated {
			return failure("REGISTRY_REMOTE_INVALID")
		}
	}
	return c.Verify(ctx, repo, d)
}
func (c *distributionClient) Remove(ctx context.Context, repo, digest string) error {
	target, e := c.endpoint(repo, "manifests", digest)
	if e != nil {
		return e
	}
	r, e := c.request(ctx, http.MethodDelete, target, "", nil, 0)
	if e != nil {
		return e
	}
	r.Body.Close()
	if r.StatusCode != http.StatusAccepted && r.StatusCode != http.StatusNotFound {
		return failure("REGISTRY_REMOTE_INVALID")
	}
	r, e = c.request(ctx, http.MethodHead, target, "", nil, 0)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		return failure("REGISTRY_REMOTE_OUTCOME_UNKNOWN")
	}
	return nil
}
func (c *distributionClient) Close() { c.http.CloseIdleConnections() }
func decimal(n int64) string         { return strconv.FormatInt(n, 10) }

func (c *distributionClient) Verify(ctx context.Context, repo string, d Descriptor) error {
	return c.verify(ctx, repo, d, false)
}
func (c *distributionClient) VerifyTag(ctx context.Context, repo string, d Descriptor) error {
	if d.Kind != "manifest" {
		return failure("REGISTRY_DESCRIPTOR_INVALID")
	}
	return c.verify(ctx, repo, d, true)
}
