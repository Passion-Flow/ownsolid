package install

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// YAML null must not turn an explicitly selected group into omission. The normal
// typed decoder still owns unknown fields and duplicate keys for the whole file.
func validatePortalListenerYAML(raw []byte) error {
	// Resolve ordinary YAML aliases and merges before checking presence, so a
	// merged explicit null cannot silently become an omitted Go pointer.
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return invalid("network.portal_listener", "must use an explicit mapping")
	}
	network, _ := document["network"].(map[string]any)
	if value, present := network["portal_listener"]; present {
		if _, mapping := value.(map[string]any); !mapping {
			return invalid("network.portal_listener", "must be a mapping; omit it for shared serving")
		}
	}
	return nil
}

func (c Config) validatePortalListener() error {
	p := c.Network.PortalListener
	if p == nil {
		return nil
	}
	if net.ParseIP(p.BindAddress) == nil {
		return invalid("network.portal_listener.bind_address", "must be an explicit IP address")
	}
	if p.HTTPSPort < 1 || p.HTTPSPort > 65535 {
		return invalid("network.portal_listener.https_port", "must be 1 to 65535")
	}
	if p.ContainerPort < 1024 || p.ContainerPort > 65535 || p.ContainerPort == 8443 {
		return invalid("network.portal_listener.container_port", "must be 1024 to 65535 and differ from the Admin container port 8443")
	}
	admin := netip.MustParseAddr(c.Network.BindAddress).Unmap()
	portal := netip.MustParseAddr(p.BindAddress).Unmap()
	if p.HTTPSPort == c.Network.HTTPSPort && addressesOverlap(admin, portal) {
		return invalid("network.portal_listener", "must not overlap the Admin host publication")
	}
	a, _ := originURL(c.Origins.Admin)
	b, _ := originURL(c.Origins.Portal)
	canonicalHost := func(host string) string {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return strings.ToLower(strings.TrimSuffix(host, "."))
	}
	if canonicalHost(a.Hostname()) == canonicalHost(b.Hostname()) {
		return invalid("origins", "separate listeners require distinct browser hostnames; shared-hostname policy is not selected by the installer")
	}
	return nil
}

func (c Config) portalListenAddress() string {
	if c.Network.PortalListener == nil {
		return ""
	}
	return net.JoinHostPort("0.0.0.0", strconv.Itoa(c.Network.PortalListener.ContainerPort))
}

// projectBrowserPorts edits only the generated copy. The default path keeps the
// shipped Compose bytes; selected projection requires the canonical base port.
func (c Config) projectBrowserPorts(raw []byte) ([]byte, error) {
	if c.Network.PortalListener == nil {
		return raw, nil
	}
	bad := errors.New("INSTALL_COMPOSE_PORTS_INVALID: select the shipped canonical Compose source with its single Admin publication")
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return nil, bad
	}
	field := func(node *yaml.Node, name string) *yaml.Node {
		if node == nil || node.Kind != yaml.MappingNode || node.Anchor != "" {
			return nil
		}
		seen := map[string]bool{}
		var found *yaml.Node
		for n := 0; n < len(node.Content); n += 2 {
			key := node.Content[n]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return nil
			}
			seen[key.Value] = true
			if key.Value == name {
				found = node.Content[n+1]
			}
		}
		return found
	}
	ports := field(field(field(document.Content[0], "services"), "control"), "ports")
	if ports == nil || ports.Kind != yaml.SequenceNode || ports.Anchor != "" || len(ports.Content) < 1 || len(ports.Content) > 2 || ports.Content[0].Anchor != "" {
		return nil, bad
	}
	var base map[string]any
	if ports.Content[0].Decode(&base) != nil || len(base) != 4 || base["target"] != 8443 ||
		base["published"] != "${OWNSOLID_HTTPS_PORT:?}" || base["host_ip"] != "${OWNSOLID_BIND_ADDRESS:?}" || base["protocol"] != "tcp" {
		return nil, bad
	}
	p := c.Network.PortalListener
	publication := map[string]any{"target": p.ContainerPort, "published": strconv.Itoa(p.HTTPSPort), "host_ip": p.BindAddress, "protocol": "tcp"}
	if len(ports.Content) == 2 {
		// Operational restore uses the installed copy. Reuse only the exact
		// selected projection; external templates still require one base port.
		var existing map[string]any
		if filepath.Clean(c.ComposeFile) != c.Layout().ComposeFile || ports.Content[1].Decode(&existing) != nil || len(existing) != 4 ||
			existing["target"] != publication["target"] || existing["published"] != publication["published"] ||
			existing["host_ip"] != publication["host_ip"] || existing["protocol"] != publication["protocol"] {
			return nil, bad
		}
		return raw, nil
	}
	var extra yaml.Node
	if err := extra.Encode(publication); err != nil {
		return nil, bad
	}
	ports.Content = append(ports.Content, &extra)
	projected, err := yaml.Marshal(&document)
	if err != nil {
		return nil, bad
	}
	return projected, nil
}

func (c Config) probeAddress(origin string) (string, error) {
	host, port := c.Network.BindAddress, c.Network.HTTPSPort
	if origin != c.Origins.Admin && origin != c.Origins.Portal {
		return "", errors.New("origin is not a configured browser entrance")
	}
	if origin == c.Origins.Portal && c.Network.PortalListener != nil {
		host, port = c.Network.PortalListener.BindAddress, c.Network.PortalListener.HTTPSPort
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("published bind address is invalid")
	}
	if ip.IsUnspecified() {
		host = "::1"
		if ip.To4() != nil {
			host = "127.0.0.1"
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}
