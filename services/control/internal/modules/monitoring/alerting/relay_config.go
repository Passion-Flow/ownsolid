package alerting

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

var (
	relayLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,95}$`)
	tenantID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	roomID     = regexp.MustCompile(`^![A-Za-z0-9._=:-]{1,254}$`)
	bearer     = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)
)

type relayConfig struct {
	ListenAddress   string         `json:"listen_address"`
	CertificateFile string         `json:"certificate_file"`
	PrivateKeyFile  string         `json:"private_key_file"`
	MatrixOrigin    string         `json:"matrix_origin"`
	MatrixCAFile    string         `json:"matrix_ca_file"`
	DeliverySeconds int            `json:"delivery_seconds"`
	MaxConcurrent   int            `json:"max_concurrent"`
	Tenants         []tenantConfig `json:"tenants"`
}

type tenantConfig struct {
	ID               string `json:"id"`
	Receiver         string `json:"receiver"`
	WebhookTokenFile string `json:"webhook_token_file"`
	MatrixTokenFile  string `json:"matrix_token_file"`
	RoomID           string `json:"room_id"`
}

type relaySettings struct {
	listen      string
	certificate tls.Certificate
	matrix      *url.URL
	roots       *x509.CertPool
	timeout     time.Duration
	concurrent  int
	tenants     map[string]tenantRoute
}

type tenantRoute struct {
	receiver string
	inbound  [32]byte
	outbound string
	room     string
}

func loadRelaySettings(filename string) (relaySettings, error) {
	raw, err := bundle.ReadProtectedConfiguration(filename)
	if err != nil {
		return relaySettings{}, errors.New("RELAY_CONFIG_FILE_INVALID")
	}
	var c relayConfig
	if release.StrictJSON(raw, &c) != nil || !privateAddress(c.ListenAddress) ||
		c.DeliverySeconds < 1 || c.DeliverySeconds > 30 || c.MaxConcurrent < 1 || c.MaxConcurrent > 64 ||
		len(c.Tenants) == 0 || len(c.Tenants) > 128 {
		return relaySettings{}, errors.New("RELAY_CONFIG_INVALID")
	}
	origin, err := url.Parse(c.MatrixOrigin)
	if err != nil || origin.Scheme != "https" || !privateAddress(origin.Host) ||
		origin.User != nil || origin.Path != "" || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" ||
		origin.String() != c.MatrixOrigin {
		return relaySettings{}, errors.New("RELAY_MATRIX_ORIGIN_INVALID")
	}
	cert, err := bundle.ReadProtectedConfiguration(c.CertificateFile)
	if err != nil {
		return relaySettings{}, errors.New("RELAY_CERTIFICATE_FILE_INVALID")
	}
	key, err := bundle.ReadProtectedConfiguration(c.PrivateKeyFile)
	if err != nil {
		return relaySettings{}, errors.New("RELAY_PRIVATE_KEY_FILE_INVALID")
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return relaySettings{}, errors.New("RELAY_TLS_KEYPAIR_INVALID")
	}
	pem, err := bundle.ReadProtectedConfiguration(c.MatrixCAFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		return relaySettings{}, errors.New("RELAY_MATRIX_CA_INVALID")
	}
	routes := make(map[string]tenantRoute, len(c.Tenants))
	rooms := make(map[string]bool, len(c.Tenants))
	tokens := make(map[[32]byte]bool, len(c.Tenants)*2)
	for i, t := range c.Tenants {
		route, err := loadTenantRoute(t)
		if err != nil {
			return relaySettings{}, fmt.Errorf("RELAY_TENANT_INVALID entry %d: %w", i+1, err)
		}
		outbound := sha256.Sum256([]byte(route.outbound))
		if _, exists := routes[t.ID]; exists || rooms[t.RoomID] || tokens[route.inbound] ||
			tokens[outbound] || route.inbound == outbound {
			return relaySettings{}, fmt.Errorf("RELAY_TENANT_BINDING_DUPLICATE entry %d", i+1)
		}
		routes[t.ID], rooms[t.RoomID] = route, true
		tokens[route.inbound], tokens[outbound] = true, true
	}
	return relaySettings{c.ListenAddress, pair, origin, roots, time.Duration(c.DeliverySeconds) * time.Second, c.MaxConcurrent, routes}, nil
}

func loadTenantRoute(t tenantConfig) (tenantRoute, error) {
	if !tenantID.MatchString(t.ID) || !relayLabel.MatchString(t.Receiver) || !roomID.MatchString(t.RoomID) {
		return tenantRoute{}, errors.New("RELAY_TENANT_SCOPE_INVALID")
	}
	in, err := readBearer(t.WebhookTokenFile, 32)
	if err != nil {
		return tenantRoute{}, errors.New("RELAY_WEBHOOK_TOKEN_INVALID")
	}
	out, err := readBearer(t.MatrixTokenFile, 16)
	if err != nil {
		return tenantRoute{}, errors.New("RELAY_MATRIX_TOKEN_INVALID")
	}
	return tenantRoute{t.Receiver, sha256.Sum256([]byte(in)), out, t.RoomID}, nil
}

func privateAddress(value string) bool {
	address, err := netip.ParseAddrPort(value)
	if err != nil || address.Port() == 0 || address.Addr().Zone() != "" {
		return false
	}
	ip := address.Addr().Unmap()
	return ip.IsPrivate() || ip.IsLoopback()
}

func readBearer(filename string, minimum int) (string, error) {
	raw, err := bundle.ReadProtectedConfiguration(filename)
	token := strings.TrimSuffix(string(raw), "\n")
	if err != nil || len(token) < minimum || len(token) > 512 || !bearer.MatchString(token) {
		return "", errors.New("RELAY_TOKEN_FILE_INVALID")
	}
	return token, nil
}
