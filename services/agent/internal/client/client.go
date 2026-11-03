package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/components"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"strings"
)

type Client struct {
	Origin          string
	HTTP            *http.Client
	Credential      string
	runtime         *settings.Policy
	preparationHTTP *http.Client
}

// Artifact uses the fixed control path and current lease; redirects/proxies remain disabled.
func (c *Client) Artifact(ctx context.Context, path, lease, destination string) error {
	return c.ArtifactVersion(ctx, path, lease, destination, wire.NodeExporterVersion)
}
func (c *Client) ArtifactVersion(ctx context.Context, path, lease, destination, version string) error {
	recipe, approved := wire.RecipeForVersion(version)
	if !approved {
		return errors.New("COMPONENT_VERSION_UNSUPPORTED")
	}
	if !strings.HasPrefix(path, "/agent/v1/nodes/") || strings.ContainsAny(path, "?#") || !strings.HasSuffix(path, "/artifact") {
		return errors.New("AGENT_PATH_INVALID")
	}
	b, _ := json.Marshal(wire.LeaseRenewal{LeaseToken: lease})
	r, e := http.NewRequestWithContext(ctx, "POST", c.Origin+path, bytes.NewReader(b))
	if e != nil {
		return errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.Credential)
	response, e := c.HTTP.Do(r)
	if e != nil {
		return errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("COMPONENT_ASSET_UNAVAILABLE")
	}
	if response.StatusCode != 200 || response.ContentLength != recipe.ArchiveBytes || response.Header.Get("X-Artifact-SHA256") != recipe.ArchiveSHA256 || response.Header.Get("Content-Type") != "application/gzip" {
		return errors.New("COMPONENT_ASSET_INVALID")
	}
	return components.DownloadToVersion(destination, response.Body, version)
}

type Problem struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}

func (p *Problem) Error() string { return p.Code }
func New(origin, caFile string, policies ...settings.Policy) (*Client, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("CONTROL_ORIGIN_INVALID")
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		b, e := os.ReadFile(caFile)
		if e != nil || !roots.AppendCertsFromPEM(b) {
			return nil, errors.New("CONTROL_CA_INVALID")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: policy.ControlConnections, IdleConnTimeout: settings.Seconds(policy.IdleConnectionSeconds), TLSHandshakeTimeout: settings.Seconds(policy.TLSHandshakeSeconds), ResponseHeaderTimeout: settings.Seconds(policy.ControlHeaderSeconds)}
	return &Client{runtime: &policy, Origin: origin, HTTP: &http.Client{Transport: transport, Timeout: settings.Seconds(policy.ControlRequestSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CONTROL_REDIRECT_REJECTED") }}}, nil
}
func (c *Client) Close() {
	c.HTTP.CloseIdleConnections()
	if c.preparationHTTP != nil {
		c.preparationHTTP.CloseIdleConnections()
	}
}
func (c *Client) Post(ctx context.Context, path string, value, out any) error {
	if !strings.HasPrefix(path, "/agent/v1/") || strings.ContainsAny(path, "?#") {
		return errors.New("AGENT_PATH_INVALID")
	}
	b, e := json.Marshal(value)
	if e != nil || len(b) > 16384 {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.Origin+path, bytes.NewReader(b))
	if e != nil {
		return errors.New("AGENT_REQUEST_INVALID")
	}
	r.Header.Set("Content-Type", "application/json")
	if c.Credential != "" {
		r.Header.Set("Authorization", "Bearer "+c.Credential)
	}
	response, e := c.HTTP.Do(r)
	if e != nil {
		return errors.New("CONTROL_CONNECTION_FAILED")
	}
	defer response.Body.Close()
	b, e = io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(b) > 65536 {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var p Problem
		if path == "/agent/v1/enroll" && json.Unmarshal(b, &p) == nil && p.Code == enrollmentCommitUnknown {
			return enrollmentUnknown(response, b)
		}
		if json.Unmarshal(b, &p) != nil || !strings.HasPrefix(p.Code, "AGENT_") && p.Code != "STORAGE_UNAVAILABLE" && p.Code != "RATE_LIMITED" && p.Code != "ACCESS_DENIED" && p.Code != "VALIDATION_FAILED" && p.Code != "TOKEN_UNUSABLE" {
			return errors.New("CONTROL_RESPONSE_REJECTED")
		}
		p.Status = response.StatusCode
		return &p
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || len(envelope.Data) == 0 {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	if out == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	return nil
}

func (c *Client) Policy() settings.Policy { return settings.Resolve(c.runtime) }
