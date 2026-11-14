package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// RequestTransport is the validated effective browser connection. SocketPeerIP
// always describes the real connection; TLS remains the actual socket TLS state.
type RequestTransport struct {
	Mode         config.TransportMode
	Audience     string
	Origin       string
	Scheme       string
	Authority    string
	ClientIP     string
	SocketPeerIP string
	Forwarded    bool
}

type requestTransportKey struct{}

func WithRequestTransport(r *http.Request, transport RequestTransport) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestTransportKey{}, transport))
}

func EffectiveRequestTransport(r *http.Request) (RequestTransport, bool) {
	value, ok := r.Context().Value(requestTransportKey{}).(RequestTransport)
	return value, ok
}

// RequestClientIP never interprets forwarding headers. A standalone internal
// handler without the dispatcher context can observe only its actual socket peer.
func RequestClientIP(r *http.Request) string {
	if value, ok := EffectiveRequestTransport(r); ok {
		return value.ClientIP
	}
	ip, e := socketPeer(r.RemoteAddr)
	if e != nil {
		return ""
	}
	return ip.String()
}

// AuditTransport correlates operational connection provenance with the request
// ID stored by business audit transactions. It never logs request headers/body,
// cookies, tokens, query strings, credentials or private configuration references.
func AuditTransport(ctx context.Context, requestID string, value RequestTransport, admitted bool) {
	slog.InfoContext(ctx, "HTTP_REQUEST_TRANSPORT", "request_id", requestID,
		"transport_mode", string(value.Mode), "audience", value.Audience,
		"authority", value.Authority, "client_ip", value.ClientIP,
		"socket_peer_ip", value.SocketPeerIP, "forwarded", value.Forwarded, "admitted", admitted)
}

// ResolveBrowserRequest requires the startup-validated server configuration.
// Every authority is selected before static assets, runtime settings or APIs.
func ResolveBrowserRequest(r *http.Request, server config.ServerConfiguration) (RequestTransport, error) {
	var value RequestTransport
	misdirected := errors.New("MISDIRECTED_REQUEST")
	refused := errors.New("ORIGIN_REJECTED")
	mode := server.EffectiveTransportMode()
	value.Scheme = "https"
	switch mode {
	case config.TransportHTTP:
		value.Scheme = "http"
	case config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS:
	default:
		return value, refused
	}
	value.Mode = mode
	authority, e := requestAuthority(value.Scheme, r.Host)
	if e != nil {
		return value, misdirected
	}
	for _, entrance := range []struct{ audience, origin string }{
		{"portal", server.PortalOrigin}, {"admin", server.AdminOrigin},
	} {
		origin, e := config.ParseBrowserOrigin(entrance.origin)
		if e != nil || origin.Scheme != value.Scheme {
			return value, refused
		}
		if authority.Authority == origin.Authority {
			if value.Audience != "" {
				return value, misdirected
			}
			value.Audience, value.Origin = entrance.audience, entrance.origin
		}
	}
	if value.Audience == "" {
		return value, misdirected
	}
	value.Authority = authority.Authority
	if r.URL == nil || r.URL.Opaque != "" || r.URL.User != nil {
		return value, misdirected
	}
	if r.URL.Host != "" {
		absolute, e := requestAuthority(value.Scheme, r.URL.Host)
		if e != nil || absolute.Authority != authority.Authority || r.URL.Scheme != value.Scheme {
			return value, misdirected
		}
	}
	peer, e := socketPeer(r.RemoteAddr)
	if e != nil {
		return value, refused
	}
	value.SocketPeerIP, value.ClientIP = peer.String(), peer.String()
	if value.Mode != config.TransportTrustedProxyHTTPS {
		if len(server.TrustedProxyCIDRs) != 0 || hasForwardingHeader(r.Header) {
			return value, refused
		}
		if value.Mode == config.TransportHTTP {
			if r.TLS != nil {
				return value, refused
			}
		} else {
			if r.TLS == nil {
				return value, refused
			}
			if _, e := netip.ParseAddr(authority.Hostname); e == nil {
				if r.TLS.ServerName != "" {
					return value, misdirected
				}
			} else if !strings.EqualFold(r.TLS.ServerName, authority.Hostname) {
				return value, misdirected
			}
		}
		return value, nil
	}
	// This initial profile has exactly one TLS-terminating proxy and a plain
	// isolated backend. A TLS backend is not silently treated as this profile.
	if r.TLS != nil || !trustedSocketPeer(peer, server.TrustedProxyCIDRs) {
		return value, refused
	}
	for key := range r.Header {
		lower := strings.ToLower(key)
		if forwardingHeader(lower) && lower != "x-forwarded-proto" && lower != "x-forwarded-host" && lower != "x-forwarded-for" {
			return value, refused
		}
	}
	proto, ok := singleHeader(r.Header, "X-Forwarded-Proto")
	if !ok || proto != "https" {
		return value, refused
	}
	host, ok := singleHeader(r.Header, "X-Forwarded-Host")
	if !ok || host != r.Host {
		return value, refused
	}
	client, ok := singleHeader(r.Header, "X-Forwarded-For")
	ip, e := netip.ParseAddr(client)
	if !ok || e != nil || ip.Zone() != "" || ip.String() != client || ip.IsUnspecified() || ip.IsMulticast() || ip.Is4In6() {
		return value, refused
	}
	value.ClientIP, value.Forwarded = ip.String(), true
	return value, nil
}

func singleHeader(headers http.Header, name string) (string, bool) {
	var found []string
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			found = append(found, values...)
		}
	}
	returnValue := ""
	if len(found) == 1 {
		returnValue = found[0]
	}
	return returnValue, len(found) == 1 && returnValue != "" && !strings.Contains(returnValue, ",")
}

func forwardingHeader(lower string) bool {
	return lower == "forwarded" || lower == "x-real-ip" || lower == "x-forwarded" || strings.HasPrefix(lower, "x-forwarded-")
}

func hasForwardingHeader(headers http.Header) bool {
	for key := range headers {
		if forwardingHeader(strings.ToLower(key)) {
			return true
		}
	}
	return false
}

func socketPeer(raw string) (netip.Addr, error) {
	host, port, e := net.SplitHostPort(raw)
	if e != nil {
		return netip.Addr{}, errors.New("SOCKET_PEER_INVALID")
	}
	n, e := strconv.ParseUint(port, 10, 16)
	if e != nil || n == 0 {
		return netip.Addr{}, errors.New("SOCKET_PEER_INVALID")
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
		return netip.Addr{}, errors.New("SOCKET_PEER_INVALID")
	}
	return ip.Unmap(), nil
}

func trustedSocketPeer(peer netip.Addr, values []string) bool {
	trusted := false
	for _, raw := range values {
		prefix, e := netip.ParsePrefix(raw)
		if e != nil || prefix.String() != raw || prefix != prefix.Masked() || prefix.Bits() == 0 || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() {
			return false
		}
		trusted = trusted || prefix.Contains(peer)
	}
	return trusted
}

// Requests may spell a default port or DNS case differently without creating a
// second origin. Configuration itself remains strictly canonical and unchanged.
func requestAuthority(scheme, raw string) (config.BrowserOrigin, error) {
	bad := errors.New("REQUEST_AUTHORITY_INVALID")
	if len(raw) > 2048 || strings.ContainsAny(raw, "/\\%?#@ \t\r\n") {
		return config.BrowserOrigin{}, bad
	}
	u, e := url.Parse(scheme + "://" + raw)
	if e != nil || u.Host != raw || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return config.BrowserOrigin{}, bad
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, e := strconv.ParseUint(port, 10, 16)
		if e != nil || n == 0 || strconv.FormatUint(n, 10) != port {
			return config.BrowserOrigin{}, bad
		}
		if scheme == "https" && port == "443" || scheme == "http" && port == "80" {
			port = ""
		}
	} else if strings.HasSuffix(raw, ":") {
		return config.BrowserOrigin{}, bad
	}
	canonical := host
	if strings.Contains(host, ":") {
		canonical = "[" + host + "]"
	}
	if port != "" {
		canonical += ":" + port
	}
	return config.ParseBrowserOrigin(scheme + "://" + canonical)
}
