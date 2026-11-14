// Package link carries one simulated agent's traffic to control: JSON over HTTPS and the
// session stream, both exactly as the contract in api/agent/v1 defines them (ADR 0007).
// It retries nothing; the agent's cycle is the one retry layer, as in the real agent.
package link

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// maximumBodyBytes bounds a request and a response body, as the agent does.
const (
	maximumRequestBytes  = 16384
	maximumResponseBytes = 65536
)

// ErrUnreachable is a request that got no answer from control.
var ErrUnreachable = errors.New("CONTROL_CONNECTION_FAILED")

// ErrInvalid is an answer that does not follow the contract.
var ErrInvalid = errors.New("CONTROL_RESPONSE_INVALID")

// Problem is a refusal control wrote. RetryAfter is its retry hint, zero when none.
type Problem struct {
	Code       string
	Status     int
	RetryAfter time.Duration
}

func (p *Problem) Error() string { return p.Code }

// RetryHint reports the retry hint of the failure e.
func RetryHint(e error) (time.Duration, bool) {
	var p *Problem
	if errors.As(e, &p) && p.RetryAfter > 0 {
		return p.RetryAfter, true
	}
	return 0, false
}

// Revoked reports whether e says the agent's credential is no longer valid.
func Revoked(e error) bool {
	var p *Problem
	return errors.As(e, &p) && p.Status == http.StatusUnauthorized
}

// Code is the code a failure is counted under.
func Code(e error) string {
	var p *Problem
	switch {
	case errors.As(e, &p):
		return p.Code
	case errors.Is(e, context.DeadlineExceeded):
		return "CONTROL_TIMEOUT"
	case errors.Is(e, context.Canceled):
		return "CANCELLED"
	}
	return e.Error()
}

// Node addresses one enrolled node.
type Node struct {
	ID         string
	Scope      wire.Scope
	Credential string
}

// Base is the cell-relative path of the node's agent calls.
func (n Node) Base() string {
	return "/agent/v1" + n.Scope.Path() + "/nodes/" + url.PathEscape(n.ID)
}

// agentConnections bounds one agent's short HTTPS calls, like a real agent's.
const agentConnections = 2

// Dialer holds what every agent of the simulator shares: the origin, TLS, and the HTTPS
// pool of task traffic.
type Dialer struct {
	origin  *url.URL
	address string
	tls     *tls.Config
	request time.Duration
	https   *http.Client
	poll    *http.Client
}

// NewDialer prepares connections to origin, verified with the system roots and caFile.
// address, when set, replaces the origin's host and port for dialing only. httpsConnections
// bounds the pool of short calls. Long-polled claims have a pool of their own, unbounded
// because each holds its connection for the whole wait, as on a real node: the agents that
// have fallen back to HTTPS bound it.
func NewDialer(origin, caFile, address string, httpsConnections int, request time.Duration) (*Dialer, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, errors.New("CONTROL_ORIGIN_INVALID")
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, e := os.ReadFile(caFile)
		if e != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CONTROL_CA_INVALID")
		}
	}
	if address == "" {
		port := u.Port()
		if port == "" {
			port = "443"
		}
		address = net.JoinHostPort(u.Hostname(), port)
	}
	d := &Dialer{origin: u, address: address, request: request, tls: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	dialer := &net.Dialer{Timeout: request}
	transport := &http.Transport{
		TLSClientConfig: d.tls.Clone(),
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		MaxIdleConns:          httpsConnections,
		MaxIdleConnsPerHost:   httpsConnections,
		MaxConnsPerHost:       httpsConnections,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   request,
		ResponseHeaderTimeout: request,
	}
	refuseRedirect := func(*http.Request, []*http.Request) error { return errors.New("CONTROL_REDIRECT_REJECTED") }
	d.https = &http.Client{Transport: transport, Timeout: request, CheckRedirect: refuseRedirect}
	// A long-polled claim may be held for the longest claim wait.
	extra := time.Duration(wire.MaxClaimWaitSeconds) * time.Second
	poll := transport.Clone()
	poll.ResponseHeaderTimeout += extra
	poll.MaxConnsPerHost = 0
	d.poll = &http.Client{Transport: poll, Timeout: request + extra, CheckRedirect: refuseRedirect}
	return d, nil
}

// ForAgent is a dialer of its own for one agent, presenting certificate (ADR 0050). Control
// binds a connection to the node its certificate names, so agents do not share one.
func (d *Dialer) ForAgent(certificate tls.Certificate) *Dialer {
	own := *d
	own.tls = d.tls.Clone()
	own.tls.Certificates = []tls.Certificate{certificate}
	https, _ := d.https.Transport.(*http.Transport)
	short := https.Clone()
	short.TLSClientConfig = own.tls.Clone()
	short.MaxIdleConns, short.MaxIdleConnsPerHost, short.MaxConnsPerHost = 1, 1, agentConnections
	own.https = &http.Client{Transport: short, Timeout: d.https.Timeout, CheckRedirect: d.https.CheckRedirect}
	poll, _ := d.poll.Transport.(*http.Transport)
	long := poll.Clone()
	long.TLSClientConfig = own.tls.Clone()
	own.poll = &http.Client{Transport: long, Timeout: d.poll.Timeout, CheckRedirect: d.poll.CheckRedirect}
	return &own
}

// Close closes the idle HTTPS connections.
func (d *Dialer) Close() {
	d.https.CloseIdleConnections()
	d.poll.CloseIdleConnections()
}

// Post sends value as JSON to the cell-relative path with the credential, and decodes the
// data of the answer into out.
func (d *Dialer) Post(ctx context.Context, path, credential string, value, out any) error {
	return d.post(ctx, d.https, path, credential, value, out)
}

// LongPoll is Post for a claim control may hold for wait seconds.
func (d *Dialer) LongPoll(ctx context.Context, path, credential string, wait int, value, out any) error {
	bounded, cancel := context.WithTimeout(ctx, d.request+time.Duration(wait)*time.Second)
	defer cancel()
	return d.post(bounded, d.poll, path, credential, value, out)
}

func (d *Dialer) post(ctx context.Context, client *http.Client, path, credential string, value, out any) error {
	body, e := json.Marshal(value)
	if e != nil || len(body) > maximumRequestBytes {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, d.origin.Scheme+"://"+d.origin.Host+path, bytes.NewReader(body))
	if e != nil {
		return errors.New("AGENT_REQUEST_INVALID")
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response, e := client.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnreachable
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if e != nil || len(raw) > maximumResponseBytes {
		return ErrInvalid
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(raw, &problem) != nil || problem.Code == "" {
			return &Problem{Code: fmt.Sprintf("HTTP_%d", response.StatusCode), Status: response.StatusCode, RetryAfter: retryAfter(response.Header)}
		}
		return &Problem{Code: problem.Code, Status: response.StatusCode, RetryAfter: retryAfter(response.Header)}
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Data) == 0 {
		return ErrInvalid
	}
	if out == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

// retryAfter reads a Retry-After header in whole seconds, bounded by the protocol.
func retryAfter(h http.Header) time.Duration {
	seconds, e := strconv.Atoi(h.Get("Retry-After"))
	if e != nil || seconds < 1 {
		return 0
	}
	return time.Duration(min(seconds, wire.MaxRetryAfterSeconds)) * time.Second
}
