package v1

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Model gateways on serving nodes (GW-114, ADR 0052). Control asks the agent of a serving node
// to start or stop the gateway of one gateway instance. To start it, the agent generates the
// gateway's credential and TLS key on the node and activates them with one lease-bound call:
// control keeps only a digest of the credential and signs a serving certificate for the origin
// the node declares. Neither secret leaves the node in any other message.
const (
	// GatewayCredentialPrefix starts every gateway credential: gw1.<gateway id>.<token>.
	GatewayCredentialPrefix = "gw1"
	// GatewayActivationPath ends the task path that activates a gateway's credential and key.
	GatewayActivationPath = "gateway-activation"
	// MaxGatewayOriginBytes bounds an origin a gateway serves or calls.
	MaxGatewayOriginBytes = 2048
	// MaxGatewayAuthorityBytes bounds the CA bundle that verifies a gateway's certificate.
	MaxGatewayAuthorityBytes = 65536
)

var gatewayActions = map[string]bool{"start": true, "stop": true}

// GatewayOperation names the task that performs a gateway action.
func GatewayOperation(action string) string { return "gateway." + action + ".v1" }

// IsGatewayOperation reports whether operation is a gateway task.
func IsGatewayOperation(operation string) bool {
	for action := range gatewayActions {
		if GatewayOperation(action) == operation {
			return true
		}
	}
	return false
}

// GatewayCommand is what control asks of a node's gateway.
type GatewayCommand struct {
	Action    string `json:"action"`
	GatewayID string `json:"gateway_id"`
	// ControlOrigin is where the gateway checks keys and reports calls: the origin its
	// instance was bound to when it was registered. Only a start carries it.
	ControlOrigin string `json:"control_origin,omitempty"`
}

// Validate checks the command against the operation that carries it.
func (c GatewayCommand) Validate(operation string) error {
	if !gatewayActions[c.Action] || GatewayOperation(c.Action) != operation {
		return errors.New("AGENT_OPERATION_UNSUPPORTED")
	}
	if !inferenceUUID.MatchString(c.GatewayID) {
		return errors.New("GATEWAY_CONFIGURATION_INVALID")
	}
	if c.Action == "start" {
		if _, e := ParseGatewayOrigin(c.ControlOrigin); e != nil {
			return errors.New("GATEWAY_CONFIGURATION_INVALID")
		}
	} else if c.ControlOrigin != "" {
		return errors.New("GATEWAY_CONFIGURATION_INVALID")
	}
	return nil
}

// GatewayActivation is the agent's one call that binds a started gateway's secrets: the
// credential it generated, a request for the key it generated, and the origin it serves.
type GatewayActivation struct {
	LeaseToken         string `json:"lease_token"`
	Credential         string `json:"credential"`
	CertificateRequest string `json:"certificate_request"`
	Origin             string `json:"origin"`
}

// Validate checks the activation's shape for the gateway being started. The request's
// signature and key are checked by ParseCertificateRequest.
func (a GatewayActivation) Validate(gateway string) error {
	if !validToken(a.LeaseToken) || !ValidGatewayCredential(a.Credential, gateway) || a.CertificateRequest == "" || len(a.CertificateRequest) > MaxCertificateRequestBytes {
		return errors.New("VALIDATION_FAILED")
	}
	if _, e := ParseGatewayOrigin(a.Origin); e != nil {
		return e
	}
	return nil
}

// GatewayActivated carries the gateway's serving certificate, leaf first, and the CA bundle
// that verifies it, both in PEM. Callers trust the same bundle.
type GatewayActivated struct {
	Certificate          string    `json:"certificate"`
	CertificateAuthority string    `json:"certificate_authority"`
	NotAfter             time.Time `json:"not_after"`
}

// GatewayObservation is what an agent saw of a gateway after acting on it.
type GatewayObservation struct {
	GatewayID   string `json:"gateway_id"`
	State       string `json:"state"`
	ContainerID string `json:"container_id,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
}

var gatewayErrors = map[string]bool{
	"GATEWAY_CONFIGURATION_INVALID": true,
	"GATEWAY_NOT_CONFIGURED":        true,
	"GATEWAY_DOCKER_UNAVAILABLE":    true,
	"GATEWAY_DOCKER_REJECTED":       true,
	"GATEWAY_IMAGE_MISSING":         true,
	"GATEWAY_IMAGE_INVALID":         true,
	"GATEWAY_ACTIVATION_REFUSED":    true,
	"GATEWAY_IO_FAILED":             true,
	"GATEWAY_CONTAINER_CONFLICT":    true,
	"GATEWAY_START_FAILED":          true,
	"GATEWAY_NOT_READY":             true,
	"GATEWAY_STOP_FAILED":           true,
	"GATEWAY_CANCELLED":             true,
}

// KnownGatewayError reports whether code is one a gateway task may fail with.
func KnownGatewayError(code string) bool { return gatewayErrors[code] }

// ValidateGateway checks a gateway task's result against its command: a start that succeeded
// left the gateway running, a stop left it stopped, and a failure names a gateway error.
func (r Result) ValidateGateway(command GatewayCommand) error {
	bad := errors.New("VALIDATION_FAILED")
	o := r.Gateway
	if command.Validate(GatewayOperation(command.Action)) != nil || r.Facts != nil || r.Component != nil || r.Logs != nil || r.Inference != nil || o == nil || o.GatewayID != command.GatewayID {
		return bad
	}
	if o.ContainerID != "" && !containerPattern.MatchString(o.ContainerID) {
		return bad
	}
	if r.Outcome == "failure" {
		if !KnownGatewayError(r.ErrorCode) || o.ErrorCode != r.ErrorCode || (o.State != "running" && o.State != "stopped") {
			return bad
		}
		return nil
	}
	want := "running"
	if command.Action == "stop" {
		want = "stopped"
	}
	if r.Outcome != "success" || r.ErrorCode != "" || o.ErrorCode != "" || o.State != want || (want == "running") != (o.ContainerID != "") {
		return bad
	}
	return nil
}

var containerPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// GatewayCredential joins a gateway's ID and a random token into its credential.
func GatewayCredential(gateway, token string) string {
	return GatewayCredentialPrefix + "." + gateway + "." + token
}

// ValidGatewayCredential reports whether credential is a well-formed credential of gateway.
func ValidGatewayCredential(credential, gateway string) bool {
	parts := strings.Split(credential, ".")
	return len(parts) == 3 && parts[0] == GatewayCredentialPrefix && parts[1] == gateway && inferenceUUID.MatchString(gateway) && validToken(parts[2])
}

// validToken accepts the protocol's random tokens: 32 bytes in unpadded base64url.
func validToken(value string) bool {
	if len(value) != 43 {
		return false
	}
	raw, e := base64.RawURLEncoding.DecodeString(value)
	return e == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

// GatewayOrigin is an HTTPS origin split into what a certificate names.
type GatewayOrigin struct {
	// Host is a lowercase DNS name, or empty when the origin names an IP address.
	Host string
	// IP is the address the origin names, or nil for a DNS name.
	IP   net.IP
	Port int
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ParseGatewayOrigin accepts only a canonical HTTPS origin: no user, path, query or fragment,
// a lowercase DNS name or an IP address, and an explicit port only when it is not 443.
func ParseGatewayOrigin(origin string) (GatewayOrigin, error) {
	bad := errors.New("GATEWAY_ORIGIN_INVALID")
	if origin == "" || len(origin) > MaxGatewayOriginBytes {
		return GatewayOrigin{}, bad
	}
	parsed, e := url.Parse(origin)
	if e != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return GatewayOrigin{}, bad
	}
	out := GatewayOrigin{Port: 443}
	if raw := parsed.Port(); raw != "" {
		port, e := strconv.Atoi(raw)
		if e != nil || port < 1 || port > 65535 || port == 443 || strconv.Itoa(port) != raw {
			return GatewayOrigin{}, bad
		}
		out.Port = port
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsUnspecified() || ip.IsMulticast() {
			return GatewayOrigin{}, bad
		}
		out.IP = ip
	} else {
		labels := strings.Split(host, ".")
		if len(host) > 253 || len(labels) < 1 {
			return GatewayOrigin{}, bad
		}
		for _, label := range labels {
			if !dnsLabel.MatchString(label) {
				return GatewayOrigin{}, bad
			}
		}
		out.Host = host
	}
	if out.String() != origin {
		return GatewayOrigin{}, bad
	}
	return out, nil
}

// String writes the origin in its canonical form.
func (o GatewayOrigin) String() string {
	host := o.Host
	if o.IP != nil {
		host = o.IP.String()
		if o.IP.To4() == nil {
			host = "[" + host + "]"
		}
	}
	if o.Port == 443 {
		return "https://" + host
	}
	return "https://" + host + ":" + strconv.Itoa(o.Port)
}
