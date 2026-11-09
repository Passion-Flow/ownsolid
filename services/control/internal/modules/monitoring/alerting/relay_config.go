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
	feishuHook = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
	bearer     = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+={0,2}$`)
)

type relayChannel uint8

const (
	matrixChannel relayChannel = iota
	feishuChannel
)

type relayConfig struct {
	ListenAddress   string         `json:"listen_address"`
	CertificateFile string         `json:"certificate_file"`
	PrivateKeyFile  string         `json:"private_key_file"`
	MatrixOrigin    string         `json:"matrix_origin,omitempty"`
	MatrixCAFile    string         `json:"matrix_ca_file,omitempty"`
	FeishuOrigin    string         `json:"feishu_origin,omitempty"`
	FeishuCAFile    string         `json:"feishu_ca_file,omitempty"`
	DeliverySeconds int            `json:"delivery_seconds"`
	MaxConcurrent   int            `json:"max_concurrent"`
	Tenants         []tenantConfig `json:"tenants"`
}

type tenantConfig struct {
	ID                      string `json:"id"`
	Receiver                string `json:"receiver"`
	WebhookTokenFile        string `json:"webhook_token_file"`
	MatrixTokenFile         string `json:"matrix_token_file,omitempty"`
	RoomID                  string `json:"room_id,omitempty"`
	FeishuHookFile          string `json:"feishu_hook_file,omitempty"`
	FeishuSigningSecretFile string `json:"feishu_signing_secret_file,omitempty"`
}

type relaySettings struct {
	listen      string
	certificate tls.Certificate
	origin      *url.URL
	channel     relayChannel
	roots       *x509.CertPool
	timeout     time.Duration
	concurrent  int
	tenants     map[string]tenantRoute
}

type tenantRoute struct {
	receiver string
	inbound  [32]byte
	outbound string
	target   string
}

func loadRelaySettings(filename string, channel relayChannel) (relaySettings, error) {
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
	endpoint, caFile := c.MatrixOrigin, c.MatrixCAFile
	originInvalid, caInvalid := "RELAY_MATRIX_ORIGIN_INVALID", "RELAY_MATRIX_CA_INVALID"
	if channel == feishuChannel {
		if endpoint != "" || caFile != "" {
			return relaySettings{}, errors.New("RELAY_CHANNEL_CONFIGURATION_MIXED")
		}
		endpoint, caFile = c.FeishuOrigin, c.FeishuCAFile
		originInvalid, caInvalid = "RELAY_FEISHU_ORIGIN_INVALID", "RELAY_FEISHU_CA_INVALID"
	} else if c.FeishuOrigin != "" || c.FeishuCAFile != "" {
		return relaySettings{}, errors.New("RELAY_CHANNEL_CONFIGURATION_MIXED")
	}
	origin, err := url.Parse(endpoint)
	if err != nil || origin.Scheme != "https" || !privateAddress(origin.Host) ||
		origin.User != nil || origin.Path != "" || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" ||
		origin.String() != endpoint {
		return relaySettings{}, errors.New(originInvalid)
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
	pem, err := bundle.ReadProtectedConfiguration(caFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		return relaySettings{}, errors.New(caInvalid)
	}
	routes := make(map[string]tenantRoute, len(c.Tenants))
	targets := make(map[string]bool, len(c.Tenants))
	tokens := make(map[[32]byte]bool, len(c.Tenants)*3)
	for i, t := range c.Tenants {
		route, err := loadTenantRoute(t, channel)
		if err != nil {
			return relaySettings{}, fmt.Errorf("RELAY_TENANT_INVALID entry %d: %w", i+1, err)
		}
		if _, exists := routes[t.ID]; exists || targets[route.target] {
			return relaySettings{}, fmt.Errorf("RELAY_TENANT_BINDING_DUPLICATE entry %d", i+1)
		}
		secrets := [][32]byte{route.inbound, sha256.Sum256([]byte(route.outbound))}
		if channel == feishuChannel {
			secrets = append(secrets, sha256.Sum256([]byte(route.target)))
		}
		for _, secret := range secrets {
			if tokens[secret] {
				return relaySettings{}, fmt.Errorf("RELAY_TENANT_BINDING_DUPLICATE entry %d", i+1)
			}
			tokens[secret] = true
		}
		routes[t.ID], targets[route.target] = route, true
	}
	return relaySettings{
		listen: c.ListenAddress, certificate: pair, origin: origin, roots: roots,
		channel: channel, timeout: time.Duration(c.DeliverySeconds) * time.Second,
		concurrent: c.MaxConcurrent, tenants: routes,
	}, nil
}

func loadTenantRoute(t tenantConfig, channel relayChannel) (tenantRoute, error) {
	if !tenantID.MatchString(t.ID) || !relayLabel.MatchString(t.Receiver) {
		return tenantRoute{}, errors.New("RELAY_TENANT_SCOPE_INVALID")
	}
	in, err := readBearer(t.WebhookTokenFile, 32)
	if err != nil {
		return tenantRoute{}, errors.New("RELAY_WEBHOOK_TOKEN_INVALID")
	}
	route := tenantRoute{receiver: t.Receiver, inbound: sha256.Sum256([]byte(in))}
	if channel == feishuChannel {
		if t.RoomID != "" || t.MatrixTokenFile != "" {
			return tenantRoute{}, errors.New("RELAY_CHANNEL_CONFIGURATION_MIXED")
		}
		hook, err := readBearer(t.FeishuHookFile, 16)
		if err != nil || !feishuHook.MatchString(hook) {
			return tenantRoute{}, errors.New("RELAY_FEISHU_HOOK_INVALID")
		}
		secret, err := readBearer(t.FeishuSigningSecretFile, 16)
		if err != nil {
			return tenantRoute{}, errors.New("RELAY_FEISHU_SECRET_INVALID")
		}
		route.target, route.outbound = hook, secret
		return route, nil
	}
	if t.FeishuHookFile != "" || t.FeishuSigningSecretFile != "" {
		return tenantRoute{}, errors.New("RELAY_CHANNEL_CONFIGURATION_MIXED")
	}
	if !roomID.MatchString(t.RoomID) {
		return tenantRoute{}, errors.New("RELAY_TENANT_SCOPE_INVALID")
	}
	out, err := readBearer(t.MatrixTokenFile, 16)
	if err != nil {
		return tenantRoute{}, errors.New("RELAY_MATRIX_TOKEN_INVALID")
	}
	route.target, route.outbound = t.RoomID, out
	return route, nil
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
