package monitoring

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func metricsQueryOrigin(raw string) *url.URL {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.Path != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.String() != raw {
		return nil
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil
	}
	if port := u.Port(); port != "" {
		value, e := strconv.ParseUint(port, 10, 16)
		if e != nil || value == 0 || strconv.FormatUint(value, 10) != port {
			return nil
		}
	}
	if u.Scheme == "https" || u.Scheme == "http" && net.ParseIP(u.Hostname()).IsLoopback() {
		return u
	}
	return nil
}

func metricsQueryAuthority(u *url.URL) string {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port)
}

// Apply the owning observation budget to connection lifetimes as well as
// request I/O. No ambient proxy or redirect may receive scoped selectors.
func newMetricsQueryClient(timeout time.Duration, concurrent int) *http.Client {
	transport := &http.Transport{
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          concurrent,
		MaxIdleConnsPerHost:   concurrent,
		MaxConnsPerHost:       concurrent,
		IdleConnTimeout:       timeout,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
