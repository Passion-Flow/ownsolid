package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

var registryUploadID = regexp.MustCompile("^[a-zA-Z0-9_-]{1,128}$")

type registryClient struct {
	base   string
	auth   registryCredential
	client *http.Client
}

func (s registrySite) client(listen string) registryClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DisableCompression: true,
		TLSClientConfig: &tls.Config{
			RootCAs: s.roots, ServerName: s.addr.Addr().String(), MinVersion: tls.VersionTLS12,
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, listen)
		},
	}
	return registryClient{
		base: "https://" + s.addr.String(),
		auth: s.auth,
		client: &http.Client{
			Transport: transport, Timeout: time.Duration(s.config.RequestSeconds) * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c registryClient) request(ctx context.Context, method, path string, body *io.SectionReader, mediaType string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = body
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, errors.New("INSTALL_REGISTRY_REQUEST_INVALID")
	}
	req.SetBasicAuth(c.auth.Username, c.auth.Password)
	if body != nil {
		req.ContentLength = body.Size()
		req.Header.Set("Content-Type", mediaType)
	}
	if mediaType != "" {
		req.Header.Set("Accept", mediaType)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("INSTALL_REGISTRY_TRANSPORT_FAILED: check site TLS, reachability and the configured request timeout")
	}
	return resp, nil
}

func (c registryClient) ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		resp, err := c.request(ctx, http.MethodGet, "/v2/", nil, "")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			return fmt.Errorf("INSTALL_REGISTRY_READINESS_FAILED: TLS endpoint rejected the Registry probe (HTTP %d)", resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return errors.New("INSTALL_REGISTRY_READINESS_FAILED: check the Registry container, private address and TLS configuration")
		case <-time.After(readinessPollInterval):
		}
	}
}

func registryPath(repo string, b bundle.RuntimeBlob) string {
	kind := "blobs"
	if b.Manifest {
		kind = "manifests"
	}
	return "/v2/" + repo + "/" + kind + "/" + b.Digest
}

func (c registryClient) upload(ctx context.Context, repo string, b bundle.RuntimeBlob, input *io.SectionReader) error {
	path := registryPath(repo, b)
	mediaType := b.MediaType
	if !b.Manifest {
		var err error
		path, err = c.blobUpload(ctx, repo, path, b.Digest)
		if err != nil || path == "" {
			return err
		}
		mediaType = "application/octet-stream"
	}
	resp, err := c.request(ctx, http.MethodPut, path, input, mediaType)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("INSTALL_REGISTRY_UPLOAD_FAILED: %s returned HTTP %d", b.Digest, resp.StatusCode)
	}
	return nil
}

func (c registryClient) blobUpload(ctx context.Context, repo, path, digest string) (string, error) {
	resp, err := c.request(ctx, http.MethodHead, path, nil, "")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "", nil
	case http.StatusNotFound:
	default:
		return "", fmt.Errorf("INSTALL_REGISTRY_BLOB_LOOKUP_FAILED: %s returned HTTP %d", digest, resp.StatusCode)
	}
	prefix := "/v2/" + repo + "/blobs/uploads/"
	resp, err = c.request(ctx, http.MethodPost, prefix, nil, "")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("INSTALL_REGISTRY_UPLOAD_START_FAILED: HTTP %d", resp.StatusCode)
	}
	location, err := resp.Location()
	if err != nil {
		return "", errors.New("INSTALL_REGISTRY_UPLOAD_LOCATION_INVALID")
	}
	return c.uploadLocation(prefix, digest, location)
}

func (c registryClient) uploadLocation(prefix, digest string, location *url.URL) (string, error) {
	id, matched := strings.CutPrefix(location.EscapedPath(), prefix)
	query, err := url.ParseQuery(location.RawQuery)
	state := query["_state"]
	if err != nil || location.User != nil || location.Fragment != "" || location.Opaque != "" ||
		location.Scheme+"://"+location.Host != c.base || !matched || !registryUploadID.MatchString(id) ||
		len(query) != 1 || len(state) != 1 || len(state[0]) == 0 || len(state[0]) > 4096 {
		return "", errors.New("INSTALL_REGISTRY_UPLOAD_LOCATION_INVALID: upload must remain in the same private repository")
	}
	query.Set("digest", digest)
	return location.EscapedPath() + "?" + query.Encode(), nil
}

func (c registryClient) verify(ctx context.Context, repo string, b bundle.RuntimeBlob) error {
	mediaType := ""
	if b.Manifest {
		mediaType = b.MediaType
	}
	resp, err := c.request(ctx, http.MethodGet, registryPath(repo, b), nil, mediaType)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("INSTALL_REGISTRY_READBACK_FAILED: %s returned HTTP %d", b.Digest, resp.StatusCode)
	}
	if b.Manifest && resp.Header.Get("Content-Type") != b.MediaType {
		return fmt.Errorf("INSTALL_REGISTRY_READBACK_INVALID: unexpected media type for %s", b.Digest)
	}
	sum := sha256.New()
	size, err := io.Copy(sum, io.LimitReader(resp.Body, b.Size+1))
	if err != nil || size != b.Size || "sha256:"+hex.EncodeToString(sum.Sum(nil)) != b.Digest {
		return fmt.Errorf("INSTALL_REGISTRY_READBACK_INVALID: content differs for %s", b.Digest)
	}
	return nil
}
