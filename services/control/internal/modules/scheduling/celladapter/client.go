// Package celladapter reads scheduler observations from a Kubernetes cell.
package celladapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

var (
	// ErrConfiguration indicates that the configured cell cannot be contacted safely.
	ErrConfiguration = errors.New("SCHEDULING_CELL_CONFIG_INVALID")
	// ErrReference indicates an invalid or unsupported object reference.
	ErrReference = errors.New("SCHEDULING_CELL_REFERENCE_INVALID")
	// ErrUnavailable indicates that the cell did not return an observation.
	ErrUnavailable = errors.New("SCHEDULING_CELL_UNAVAILABLE")
	// ErrForbidden indicates a cell service account authentication or RBAC failure.
	ErrForbidden = errors.New("SCHEDULING_CELL_FORBIDDEN")
	// ErrMissing indicates the referenced object is absent from the cell API.
	ErrMissing = errors.New("SCHEDULING_CELL_OBJECT_MISSING")
	// ErrReplaced indicates that a different object now has the requested name.
	ErrReplaced = errors.New("SCHEDULING_CELL_OBJECT_REPLACED")
	// ErrProtocol indicates that the cell returned a malformed observation.
	ErrProtocol = errors.New("SCHEDULING_CELL_RESPONSE_INVALID")
)

var (
	labelPattern  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	bearerPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)
)

// Config names an operator-configured cell API and protected credential files.
type Config struct {
	Origin        string
	DialAddress   netip.Addr
	CAFile        string
	TokenFile     string
	Timeout       time.Duration
	MaxConcurrent int
}

// Client reads only the scheduler resources allowed by its service account.
type Client struct {
	origin *url.URL
	http   *http.Client
	token  string
}

// New constructs a cell client with certificate verification and no HTTP proxy.
func New(cfg Config) (*Client, error) {
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil ||
		(origin.Path != "" && origin.Path != "/") || origin.RawPath != "" ||
		origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" ||
		!cfg.DialAddress.IsValid() || cfg.DialAddress.Zone() != "" ||
		(!cfg.DialAddress.Unmap().IsPrivate() && !cfg.DialAddress.Unmap().IsLoopback()) ||
		cfg.Timeout <= 0 || cfg.MaxConcurrent <= 0 {
		return nil, ErrConfiguration
	}
	authority, err := bundle.ReadProtectedConfiguration(cfg.CAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(authority) {
		return nil, ErrConfiguration
	}
	token, err := bundle.ReadProtectedConfiguration(cfg.TokenFile)
	token = bytes.TrimSpace(token)
	if err != nil || len(token) == 0 || len(token) > 8192 || !bearerPattern.Match(token) {
		return nil, ErrConfiguration
	}
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	address := net.JoinHostPort(cfg.DialAddress.Unmap().String(), port)
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: origin.Hostname()},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		},
		DisableCompression:  true,
		MaxConnsPerHost:     cfg.MaxConcurrent,
		MaxIdleConnsPerHost: cfg.MaxConcurrent,
		IdleConnTimeout:     cfg.Timeout,
	}
	return &Client{
		origin: origin, token: string(token),
		http: &http.Client{
			Transport: transport, Timeout: cfg.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// CloseIdleConnections releases pooled connections for this cell.
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }

// Scheduler selects the Kubernetes API that owns the workload.
type Scheduler string

const (
	// Kueue observes a kueue.x-k8s.io/v1beta2 Workload.
	Kueue Scheduler = "kueue"
	// Volcano observes a batch.volcano.sh/v1alpha1 Job.
	Volcano Scheduler = "volcano"
)

// Reference binds an observation to the exact object created for an attempt.
type Reference struct {
	Scheduler Scheduler
	Namespace string
	Name      string
	UID       string
}

type endpoint struct {
	version, kind, resource string
}

func (r Reference) endpoint() (endpoint, error) {
	if !labelPattern.MatchString(r.Namespace) || r.UID == "" || len(r.UID) > 128 || len(r.Name) > 253 {
		return endpoint{}, ErrReference
	}
	for _, part := range strings.Split(r.Name, ".") {
		if !labelPattern.MatchString(part) {
			return endpoint{}, ErrReference
		}
	}
	switch r.Scheduler {
	case Kueue:
		return endpoint{"kueue.x-k8s.io/v1beta2", "Workload", "workloads"}, nil
	case Volcano:
		return endpoint{"batch.volcano.sh/v1alpha1", "Job", "jobs"}, nil
	default:
		return endpoint{}, ErrReference
	}
}

// Observe reads the current scheduler state of a specific cell object.
func (c *Client) Observe(ctx context.Context, r Reference) (Observation, error) {
	ep, err := r.endpoint()
	if err != nil {
		return Observation{}, err
	}
	u := *c.origin
	u.Path = "/apis/" + ep.version + "/namespaces/" + r.Namespace + "/" + ep.resource + "/" + r.Name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Observation{}, ErrReference
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Observation{}, ctx.Err()
		}
		return Observation{}, ErrUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return Observation{}, ErrForbidden
	case http.StatusNotFound:
		return Observation{}, ErrMissing
	default:
		return Observation{}, ErrUnavailable
	}
	const maxObservationBytes = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxObservationBytes+1))
	if err != nil {
		return Observation{}, ErrUnavailable
	}
	var obj object
	if len(raw) > maxObservationBytes || json.Unmarshal(raw, &obj) != nil ||
		obj.APIVersion != ep.version || obj.Kind != ep.kind ||
		obj.Metadata.Namespace != r.Namespace || obj.Metadata.Name != r.Name ||
		obj.Metadata.UID == "" || obj.Metadata.ResourceVersion == "" {
		return Observation{}, ErrProtocol
	}
	if obj.Metadata.UID != r.UID {
		return Observation{}, ErrReplaced
	}
	return obj.observation(r.Scheduler), nil
}
