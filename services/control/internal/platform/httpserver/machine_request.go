package httpserver

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// ResolveMachineRequest admits the dedicated direct-TLS socket only. It does
// not grant a node credential, lease, gateway, Workspace or License authority.
func ResolveMachineRequest(r *http.Request, server config.MachineServerConfiguration) (RequestTransport, error) {
	value := RequestTransport{Mode: config.TransportDirectHTTPS, Audience: "admin", Origin: server.Origin, Scheme: "https"}
	refused := errors.New("ORIGIN_REJECTED")
	misdirected := errors.New("MISDIRECTED_REQUEST")
	if server.Validate() != nil || r == nil || r.TLS == nil {
		return value, refused
	}
	origin, e := config.ParseBrowserOrigin(server.Origin)
	if e != nil || origin.Scheme != "https" {
		return value, refused
	}
	authority, e := requestAuthority("https", r.Host)
	if e != nil || authority != origin || r.URL == nil || r.URL.Opaque != "" || r.URL.User != nil || r.URL.Scheme != "" && r.URL.Host == "" {
		return value, misdirected
	}
	value.Authority = authority.Authority
	if r.URL.Host != "" {
		absolute, e := requestAuthority("https", r.URL.Host)
		if e != nil || absolute != origin || r.URL.Scheme != "https" {
			return value, misdirected
		}
	}
	if _, e := netip.ParseAddr(origin.Hostname); e == nil {
		if r.TLS.ServerName != "" {
			return value, misdirected
		}
	} else if !strings.EqualFold(r.TLS.ServerName, origin.Hostname) {
		return value, misdirected
	}
	peer, e := socketPeer(r.RemoteAddr)
	if e != nil {
		return value, refused
	}
	value.SocketPeerIP, value.ClientIP = peer.String(), peer.String()
	for key := range r.Header {
		if forwardingHeader(strings.ToLower(key)) || strings.EqualFold(key, "Origin") || strings.EqualFold(key, "Cookie") {
			return value, refused
		}
	}
	return value, nil
}
