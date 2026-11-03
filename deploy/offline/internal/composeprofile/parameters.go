// Package composeprofile validates explicit ordinary Compose port intent only.
// It does not load runtime configuration, contact an Engine or read ambient input.
package composeprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	maximumConfigurationBytes = 64 << 10
	maximumEnvironmentBytes   = 16 << 10
	formatVersion             = 1
	minimumPort               = 1
	maximumPort               = 65535
	valuesInvalid             = "COMPOSE_PORT_VALUES_INVALID"
	environmentInvalid        = "COMPOSE_PORT_ENVIRONMENT_INVALID"
	bindAddressEnvironment    = "PROJECT_ONE_BIND_ADDRESS"
	publishedPortEnvironment  = "PROJECT_ONE_HTTPS_PORT"
	targetPortEnvironment     = "PROJECT_ONE_CONTROL_PORT"
)

// Parameters is complete, public port-rendering intent. Template defaults are
// not duplicated here, and the historical published-port name has no alias.
type Parameters struct {
	FormatVersion int    `json:"format_version"`
	BindAddress   string `json:"bind_address"`
	PublishedPort int    `json:"published_port"`
	TargetPort    int    `json:"target_port"`
}

func (p Parameters) validate() error {
	address, e := netip.ParseAddr(p.BindAddress)
	if p.FormatVersion != formatVersion || e != nil || address.Zone() != "" || address.Is4In6() || address.String() != p.BindAddress || !(address.IsUnspecified() || address.IsLoopback() || address.IsGlobalUnicast()) || p.PublishedPort < minimumPort || p.PublishedPort > maximumPort || p.TargetPort < minimumPort || p.TargetPort > maximumPort {
		return errors.New(valuesInvalid)
	}
	return nil
}

// Load validates the complete JSON base before applying an explicit optional
// environment overlay. A malformed base cannot be repaired by its overlay.
func Load(raw, overlay []byte) (Parameters, error) {
	var p Parameters
	var members map[string]json.RawMessage
	if len(raw) == 0 || len(raw) > maximumConfigurationBytes || !utf8.Valid(raw) || wire.StrictJSON(raw, &members) != nil || len(members) != 4 {
		return Parameters{}, errors.New(valuesInvalid)
	}
	for _, key := range []string{"format_version", "bind_address", "published_port", "target_port"} {
		if _, present := members[key]; !present {
			return Parameters{}, errors.New(valuesInvalid)
		}
	}
	if wire.StrictJSON(raw, &p) != nil || p.validate() != nil {
		return Parameters{}, errors.New(valuesInvalid)
	}
	if len(overlay) > maximumEnvironmentBytes || !utf8.Valid(overlay) || bytes.IndexByte(overlay, 0) >= 0 {
		return Parameters{}, errors.New(environmentInvalid)
	}
	seen := make(map[string]bool, 3)
	for _, rawLine := range bytes.Split(overlay, []byte{'\n'}) {
		line := strings.TrimSuffix(string(rawLine), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || seen[key] || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(line, "\r\x00") {
			return Parameters{}, errors.New(environmentInvalid)
		}
		switch key {
		case bindAddressEnvironment, publishedPortEnvironment, targetPortEnvironment:
		default:
			return Parameters{}, errors.New(environmentInvalid)
		}
		seen[key] = true
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if wire.StrictJSON([]byte(value), &decoded) != nil || decoded == "" {
				return Parameters{}, errors.New(environmentInvalid)
			}
			value = decoded
		}
		switch key {
		case bindAddressEnvironment:
			p.BindAddress = value
		case publishedPortEnvironment, targetPortEnvironment:
			port, e := strconv.Atoi(value)
			if e != nil || port < minimumPort || port > maximumPort || strconv.Itoa(port) != value {
				return Parameters{}, errors.New(environmentInvalid)
			}
			if key == publishedPortEnvironment {
				p.PublishedPort = port
			} else {
				p.TargetPort = port
			}
		}
	}
	if p.validate() != nil {
		return Parameters{}, errors.New(environmentInvalid)
	}
	return p, nil
}

// Values emits only a public final override. The !override tag replaces raw
// interpolated bindings instead of appending an additional unvalidated port.
func (p Parameters) Values() ([]byte, error) {
	if e := p.validate(); e != nil {
		return nil, e
	}
	address, _ := json.Marshal(p.BindAddress)
	published, _ := json.Marshal(strconv.Itoa(p.PublishedPort))
	return []byte(fmt.Sprintf("services:\n  control:\n    ports: !override\n      - target: %d\n        published: %s\n        host_ip: %s\n        protocol: \"tcp\"\n", p.TargetPort, published, address)), nil
}
