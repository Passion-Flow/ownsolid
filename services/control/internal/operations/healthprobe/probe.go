// Package healthprobe performs a single verified, read-only control health request.
package healthprobe

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const (
	Command = "health-probe"
	Success = "CONTROL_HEALTH_PROBE_OK"

	argumentInvalid      = "HEALTH_PROBE_ARGUMENT_INVALID"
	configurationInvalid = "HEALTH_PROBE_CONFIGURATION_INVALID"
	trustInvalid         = "HEALTH_PROBE_TRUST_INVALID"
	transportFailed      = "HEALTH_PROBE_TRANSPORT_FAILED"
	deadlineExceeded     = "HEALTH_PROBE_DEADLINE_EXCEEDED"
	cancelled            = "HEALTH_PROBE_CANCELLED"
	responseInvalid      = "HEALTH_PROBE_RESPONSE_INVALID"

	maximumResponseBytes  int64 = 1024
	maximumTimeoutSeconds       = 60
)

// Input has no defaults or environment authority. CAFile is a private reference,
// not a PEM value. The serving configuration owns the advertised URL separately
// from DialAddress, which selects only this invocation's numeric socket target.
type Input struct {
	Audience       string
	Kind           string
	DialAddress    string
	CAFile         string
	TimeoutSeconds int
}

// ValidateInput is independent of serving configuration and performs no I/O.
// The CLI calls it before reading any configuration or opening runtime owners.
func ValidateInput(input Input) error {
	if input.Audience != "portal" && input.Audience != "admin" && input.Audience != "machine" || input.Kind != "live" && input.Kind != "ready" || input.TimeoutSeconds < 1 || input.TimeoutSeconds > maximumTimeoutSeconds {
		return errors.New(argumentInvalid)
	}
	host, port, e := net.SplitHostPort(input.DialAddress)
	if e != nil {
		return errors.New(argumentInvalid)
	}
	ip, e := netip.ParseAddr(host)
	n, pe := strconv.ParseUint(port, 10, 16)
	if e != nil || ip.Zone() != "" || ip.Is4In6() || ip.String() != host || !(ip.IsGlobalUnicast() || ip.IsLoopback()) || pe != nil || n == 0 || strconv.FormatUint(n, 10) != port || net.JoinHostPort(host, port) != input.DialAddress {
		return errors.New(argumentInvalid)
	}
	if input.CAFile != "" && !privateReference(input.CAFile) {
		return errors.New(argumentInvalid)
	}
	return nil
}

func privateReference(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) != string(filepath.Separator) && !strings.ContainsFunc(path, unicode.IsControl)
}

func contextFailure(ctx context.Context, cause error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(cause, context.DeadlineExceeded) {
		return errors.New(deadlineExceeded)
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(cause, context.Canceled) {
		return errors.New(cancelled)
	}
	var timeout net.Error
	if errors.As(cause, &timeout) && timeout.Timeout() {
		return errors.New(deadlineExceeded)
	}
	return nil
}

// Run never opens SQL, workers or serving assets. It preserves the selected URL
// authority/reference identity while dialing one explicitly selected socket.
// Every error is a fixed code; input values and underlying URL/path/TLS errors
// are deliberately not wrapped or returned.
func Run(ctx context.Context, server config.ServerConfiguration, input Input) error {
	return RunWithMachine(ctx, server, nil, input)
}

// RunWithMachine selects only a configured dedicated HTTPS origin for machine
// input. It never falls back to browser Admin, even if that origin is secure.
// All audiences share the same private trust, single-request and response path.
func RunWithMachine(ctx context.Context, server config.ServerConfiguration, machine *config.MachineServerConfiguration, input Input) error {
	if ctx == nil {
		return errors.New(argumentInvalid)
	}
	if e := ValidateInput(input); e != nil {
		return e
	}
	if server.ValidateTransport() != nil {
		return errors.New(configurationInvalid)
	}
	origin := server.PortalOrigin
	if input.Audience == "admin" {
		origin = server.AdminOrigin
	}
	if input.Audience == "machine" {
		if machine == nil || machine.Validate() != nil || machine.ListenAddress == server.ListenAddress || machine.Origin == server.PortalOrigin || machine.Origin == server.AdminOrigin {
			return errors.New(configurationInvalid)
		}
		origin = machine.Origin
	}
	reference, e := config.ParseBrowserOrigin(origin)
	if e != nil {
		return errors.New(configurationInvalid)
	}
	secure := reference.Scheme == "https"
	if secure && input.CAFile == "" || !secure && input.CAFile != "" {
		return errors.New(argumentInvalid)
	}
	if e := contextFailure(ctx, nil); e != nil {
		return e
	}
	timeout := time.Duration(input.TimeoutSeconds) * time.Second
	operation, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		ForceAttemptHTTP2:     false,
		MaxConnsPerHost:       1,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	defer transport.CloseIdleConnections()
	if secure {
		roots, e := readTrustPool(input.CAFile)
		if failure := contextFailure(operation, nil); failure != nil {
			return failure
		}
		if e != nil {
			return errors.New(trustInvalid)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: reference.Hostname, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	}
	if e := contextFailure(operation, nil); e != nil {
		return e
	}
	expected := net.JoinHostPort(reference.Hostname, strconv.FormatUint(uint64(reference.Port), 10))
	var dials atomic.Uint32
	dialer := &net.Dialer{Timeout: timeout}
	transport.DialContext = func(dialContext context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != expected || dials.Add(1) != 1 {
			return nil, errors.New(transportFailed)
		}
		return dialer.DialContext(dialContext, "tcp", input.DialAddress)
	}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, e := http.NewRequestWithContext(operation, http.MethodGet, origin+"/health/"+input.Kind, nil)
	if e != nil {
		return errors.New(configurationInvalid)
	}
	request.Header.Set("Accept", "application/json")
	response, e := client.Do(request)
	if e != nil {
		if failure := contextFailure(operation, e); failure != nil {
			return failure
		}
		return errors.New(transportFailed)
	}
	defer response.Body.Close()
	media, _, me := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || me != nil || media != "application/json" || len(response.Header.Values("Content-Type")) != 1 || len(response.Header.Values("Cache-Control")) != 1 || response.Header.Get("Cache-Control") != "no-store" || len(response.Header.Values("X-Request-ID")) != 1 || strings.TrimSpace(response.Header.Get("X-Request-ID")) == "" || response.ContentLength > maximumResponseBytes {
		return errors.New(responseInvalid)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, maximumResponseBytes+1))
	if failure := contextFailure(operation, e); failure != nil {
		return failure
	}
	if e != nil || len(raw) == 0 || int64(len(raw)) > maximumResponseBytes || !exactEnvelope(raw) {
		return errors.New(responseInvalid)
	}
	return nil
}

// Reading the fixed token sequence rejects duplicates, unknown fields, nulls,
// other status values and trailing JSON without an alternate schema authority.
func exactEnvelope(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for _, expected := range []any{json.Delim('{'), "data", json.Delim('{'), "status", "ok", json.Delim('}'), json.Delim('}')} {
		actual, e := decoder.Token()
		if e != nil || actual != expected {
			return false
		}
	}
	_, e := decoder.Token()
	return e == io.EOF
}
