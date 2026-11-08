package cli

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
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
)

const authPath = "/api/v1/auth"

type apiClient struct {
	origin url.URL
	http   *http.Client
	prefix string
}

func newClient(cfg settings) (*apiClient, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if runtime.GOOS == "darwin" && cfg.origin.Scheme == "https" && cfg.CAFile == "" {
		return nil, errors.New("HTTPS on macOS needs a local installation CA; set ca_file in config.json")
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read installation CA file: %w", err)
		}
		// An explicit pool uses the Go verifier without platform trust lookups.
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("ca_file must contain a PEM certificate for the installation CA")
		}
		tlsConfig.RootCAs = roots
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	transport.Proxy = nil

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	prefix := "__Host-admin_"
	if cfg.origin.Scheme == "http" {
		prefix = "projectone_http_admin_"
	}
	return &apiClient{
		origin: cfg.origin,
		prefix: prefix,
		http: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   cfg.timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *apiClient) restoreSession(value string) {
	if value != "" {
		c.http.Jar.SetCookies(&c.origin, []*http.Cookie{{
			Name: c.prefix + "session", Value: value, Path: "/",
			Secure: c.origin.Scheme == "https",
		}})
	}
}

func (c *apiClient) cookie(purpose string) string {
	for _, cookie := range c.http.Jar.Cookies(&c.origin) {
		if cookie.Name == c.prefix+purpose {
			return cookie.Value
		}
	}
	return ""
}

func (c *apiClient) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.request(ctx, http.MethodGet, path, query, "", nil, out)
}

func (c *apiClient) authPost(ctx context.Context, operation string, body []byte, out any) error {
	var csrf struct {
		Token string `json:"token"`
	}
	if err := c.get(ctx, authPath+"/csrf", nil, &csrf); err != nil {
		return err
	}
	if csrf.Token == "" || len(csrf.Token) > 256 || c.cookie("csrf_context") == "" {
		return fmt.Errorf("server %s did not issue an Admin CSRF token and context cookie; check that --server points to the Admin entrance", c.origin.String())
	}
	return c.request(ctx, http.MethodPost, authPath+"/"+operation, nil, csrf.Token, body, out)
}

func (c *apiClient) request(ctx context.Context, method, path string, query url.Values, csrf string, body []byte, out any) error {
	target := c.origin
	target.Path, target.RawQuery = path, query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot construct the request to %s", c.origin.String())
	}
	req.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("Origin", c.origin.String())
		req.Header.Set("X-CSRF-Token", csrf)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return connectionError(c.origin.String(), err)
	}
	defer res.Body.Close()
	expected := http.StatusOK
	if out == nil {
		expected = http.StatusNoContent
	}
	if res.StatusCode != expected {
		return newAPIError(c.origin.String(), res)
	}
	if out == nil {
		return nil
	}
	const maxResponse = 32 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return connectionError(c.origin.String(), err)
	}
	if len(data) > maxResponse {
		return fmt.Errorf("response from %s exceeds 32 MiB; check the server version and request a smaller scope", c.origin.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Data) == 0 ||
		bytes.Equal(envelope.Data, []byte("null")) || json.Unmarshal(envelope.Data, out) != nil {
		return fmt.Errorf("unexpected API response from %s; check the Admin origin and CLI/server version compatibility", c.origin.String())
	}
	return nil
}

func connectionError(origin string, err error) error {
	var cert *tls.CertificateVerificationError
	var dns *net.DNSError
	var network net.Error
	reason := "the connection failed; check the Admin address, network route, and server status"
	switch {
	case errors.Is(err, context.Canceled):
		reason = "the request was interrupted; retry when ready"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		reason = "the request timed out; check the intranet route and server load, then retry"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "the connection was refused; check that the Admin service is running and listening on this port, then retry"
	case errors.As(err, &cert):
		reason = "TLS certificate verification failed; check the server hostname, clock, and installation CA (ca_file in config.json)"
	case errors.As(err, &dns):
		reason = "the hostname could not be resolved; check the Admin address and intranet DNS"
	}
	return fmt.Errorf("cannot complete a request to %s: %s", origin, reason)
}

type apiError struct {
	origin, code, requestID string
	status                  int
	retryAfter              uint64
}

var problemCode = regexp.MustCompile("^[A-Z][A-Z_0-9]{0,79}$")

func newAPIError(origin string, res *http.Response) *apiError {
	result := &apiError{origin: origin, status: res.StatusCode}
	var problem struct {
		Code string `json:"code"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&problem) == nil && problemCode.MatchString(problem.Code) {
		result.code = problem.Code
	}
	if id := res.Header.Get("X-Request-ID"); resourceID.MatchString(id) {
		result.requestID = id
	}
	result.retryAfter, _ = strconv.ParseUint(res.Header.Get("Retry-After"), 10, 32)
	return result
}

func (e *apiError) Error() string {
	detail := "check the Admin origin and server configuration"
	switch {
	case e.status >= 300 && e.status < 400:
		detail = "redirects are refused; set --server to the final Admin origin"
	case e.code == "AUTH_INVALID_CREDENTIALS":
		detail = "check the account email and password and confirm it has Admin access"
	case e.code == "ORIGIN_REJECTED":
		detail = "use the exact advertised Admin origin in --server"
	case e.code == "CSRF_INVALID":
		detail = "the server rejected the CSRF check; retry the command and check the Admin origin if it persists"
	case e.status == http.StatusUnauthorized:
		detail = "the Admin session is invalid or expired; run ownsolid login"
	case e.status == http.StatusForbidden:
		detail = "check your Admin permissions and the installation license"
	case e.status == http.StatusNotFound:
		detail = "check the resource ID and the selected site, cell, and workspace"
	case e.status == http.StatusTooManyRequests:
		detail = "the server is rate limiting requests; wait and retry"
	case e.status >= 500:
		detail = "the server could not handle the request; check service health and retry"
	}
	message := fmt.Sprintf("%s returned HTTP %d", e.origin, e.status)
	if e.code != "" {
		message += " " + e.code
	}
	if e.requestID != "" {
		message += " (request " + e.requestID + ")"
	}
	if e.retryAfter != 0 {
		detail += fmt.Sprintf(" after %d seconds", e.retryAfter)
	}
	return message + ": " + detail
}
