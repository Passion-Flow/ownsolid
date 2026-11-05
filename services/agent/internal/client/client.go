package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/components"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Scope           wire.Scope
	Origin          string
	HTTP            *http.Client
	Credential      string
	runtime         *settings.Policy
	preparationHTTP *http.Client
	// tls verifies control for the session stream with the same roots as HTTP.
	tls *tls.Config
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
	r, e := http.NewRequestWithContext(ctx, "POST", c.target(path), bytes.NewReader(b))
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

// Problem is a failure control reported. RetryAfter is control's retry hint, from the
// Retry-After header or the session problem, and zero when control gave none (ADR 0018).
type Problem struct {
	Code       string        `json:"code"`
	Status     int           `json:"status"`
	RetryAfter time.Duration `json:"-"`
}

func (p *Problem) Error() string { return p.Code }

// RetryHint reports the retry hint control sent with the failure e.
func RetryHint(e error) (time.Duration, bool) {
	var p *Problem
	if errors.As(e, &p) && p.RetryAfter > 0 {
		return p.RetryAfter, true
	}
	return 0, false
}

// hintSpreadPercent spreads a wait after a hint above the hint itself, so agents refused
// together with one hint do not return together.
const hintSpreadPercent = 50

// AfterHint is how long to wait after a failure with the retry hint: uniform between the
// hint and the hint plus hintSpreadPercent, never less than the hint.
func AfterHint(hint time.Duration) time.Duration {
	return afterHint(rand.Int64N, hint)
}

func afterHint(random func(int64) int64, hint time.Duration) time.Duration {
	return hint + time.Duration(random(int64(hint)*hintSpreadPercent/100+1))
}

// retryAfter reads a Retry-After header given in whole seconds, bounded by the protocol.
// A date, a malformed value or none is no hint.
func retryAfter(h http.Header) time.Duration {
	seconds, e := strconv.Atoi(h.Get("Retry-After"))
	if e != nil || seconds < 1 {
		return 0
	}
	return time.Duration(min(seconds, wire.MaxRetryAfterSeconds)) * time.Second
}
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
	verify := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport := &http.Transport{TLSClientConfig: verify.Clone(), MaxIdleConns: 1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: policy.ControlConnections, IdleConnTimeout: settings.Seconds(policy.IdleConnectionSeconds), TLSHandshakeTimeout: settings.Seconds(policy.TLSHandshakeSeconds), ResponseHeaderTimeout: settings.Seconds(policy.ControlHeaderSeconds)}
	return &Client{tls: verify, runtime: &policy, Scope: wire.LocalScope(), Origin: origin, HTTP: &http.Client{Transport: transport, Timeout: settings.Seconds(policy.ControlRequestSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("CONTROL_REDIRECT_REJECTED") }}}, nil
}

// target addresses a cell-relative path in this node's cell. Paths stay cell-relative in
// the agent, and only enrollment, which happens before a node has a cell, has no scope.
func (c *Client) target(path string) string {
	if rest, found := strings.CutPrefix(path, "/agent/v1/nodes/"); found {
		return c.Origin + "/agent/v1" + c.Scope.Path() + "/nodes/" + rest
	}
	return c.Origin + path
}
func (c *Client) Close() {
	c.HTTP.CloseIdleConnections()
	if c.preparationHTTP != nil {
		c.preparationHTTP.CloseIdleConnections()
	}
}
func (c *Client) Post(ctx context.Context, path string, value, out any) error {
	return c.post(ctx, c.HTTP, path, value, out)
}

// remoteProblem keeps a control failure only when its code is one the agent acts on.
// Its retry hint survives either way, so a refusal still paces the agent.
func remoteProblem(code string, status int, retry time.Duration) error {
	if !strings.HasPrefix(code, "AGENT_") && code != "STORAGE_UNAVAILABLE" && code != "RATE_LIMITED" && code != "ACCESS_DENIED" && code != "VALIDATION_FAILED" && code != "TOKEN_UNUSABLE" {
		code = "CONTROL_RESPONSE_REJECTED"
		if retry == 0 {
			return errors.New(code)
		}
	}
	return &Problem{Code: code, Status: status, RetryAfter: retry}
}

func (c *Client) post(ctx context.Context, client *http.Client, path string, value, out any) error {
	if !strings.HasPrefix(path, "/agent/v1/") || strings.ContainsAny(path, "?#") {
		return errors.New("AGENT_PATH_INVALID")
	}
	b, e := json.Marshal(value)
	if e != nil || len(b) > 16384 {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.target(path), bytes.NewReader(b))
	if e != nil {
		return errors.New("AGENT_REQUEST_INVALID")
	}
	r.Header.Set("Content-Type", "application/json")
	if c.Credential != "" {
		r.Header.Set("Authorization", "Bearer "+c.Credential)
	}
	response, e := client.Do(r)
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
		if json.Unmarshal(b, &p) != nil {
			return errors.New("CONTROL_RESPONSE_REJECTED")
		}
		return remoteProblem(p.Code, response.StatusCode, retryAfter(response.Header))
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
