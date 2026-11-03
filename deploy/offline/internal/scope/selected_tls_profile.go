package scope

import (
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type selectedTLSProfile struct {
	CertificateName string
	PrivateKeyName  string
	Origins         []string
}

const (
	selectedTLSUnsupportedProfile    = "OFFLINE_SCOPE_TLS_SELECTED_PROFILE_UNSUPPORTED"
	selectedTLSMaximumOriginBytes    = 2048
	selectedTLSMaximumReferenceBytes = 1024
	selectedTLSMaximumHostnameBytes  = 253
	selectedTLSMaximumDNSLabelBytes  = 63
	selectedTLSDefaultHTTPSPort      = 443
)

func selectedTLSObject(raw json.RawMessage, required, optional []string) (map[string]string, error) {
	var values map[string]string
	if wire.StrictJSON(raw, &values) != nil || values == nil {
		return nil, failure(selectedTLSUnsupportedProfile)
	}
	for key := range values {
		if !slices.Contains(required, key) && !slices.Contains(optional, key) {
			return nil, failure(selectedTLSUnsupportedProfile)
		}
	}
	for _, key := range required {
		if values[key] == "" || strings.ContainsFunc(values[key], unicode.IsControl) {
			return nil, failure(selectedTLSUnsupportedProfile)
		}
	}
	return values, nil
}

func selectedTLSPath(value string) bool {
	return utf8.ValidString(value) && strings.HasPrefix(value, "/") && value != "/" && path.Clean(value) == value && !strings.ContainsAny(value, "\\:$") && !strings.ContainsFunc(value, unicode.IsControl)
}

func selectedTLSOverlaps(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

// These are literal address checks, not DNS resolution or a socket-overlap proof.
func selectedTLSHostname(host string, allowUnspecified bool) bool {
	if host == "" || len(host) > selectedTLSMaximumHostnameBytes || strings.ToLower(host) != host {
		return false
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		return ip.Zone() == "" && !ip.Is4In6() && ip.String() == host && (ip.IsGlobalUnicast() || ip.IsLoopback() || allowUnspecified && ip.IsUnspecified())
	}
	labels := strings.Split(host, ".")
	last := labels[len(labels)-1]
	numeric := last != ""
	for _, value := range last {
		numeric = numeric && value >= '0' && value <= '9'
	}
	if numeric {
		return false
	}
	if strings.HasPrefix(last, "0x") {
		hexadecimal := true
		for _, value := range last[2:] {
			hexadecimal = hexadecimal && (value >= '0' && value <= '9' || value >= 'a' && value <= 'f')
		}
		if hexadecimal {
			return false
		}
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > selectedTLSMaximumDNSLabelBytes || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, value := range label {
			if !(value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '-') {
				return false
			}
		}
	}
	return true
}

func selectedTLSListener(value string) bool {
	host, port, e := net.SplitHostPort(value)
	n, pe := strconv.ParseUint(port, 10, 16)
	return e == nil && pe == nil && n > 0 && strconv.FormatUint(n, 10) == port && selectedTLSHostname(host, true) && net.JoinHostPort(host, port) == value
}

func selectedTLSOrigin(value string) bool {
	if len(value) > selectedTLSMaximumOriginBytes || strings.ContainsAny(value, "\\%?#") || strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return false
	}
	u, e := url.Parse(value)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != value || !selectedTLSHostname(u.Hostname(), false) {
		return false
	}
	host := u.Hostname()
	wanted := host
	if strings.Contains(host, ":") {
		wanted = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		n, pe := strconv.ParseUint(port, 10, 16)
		if pe != nil || n == 0 || n == selectedTLSDefaultHTTPSPort || strconv.FormatUint(n, 10) != port {
			return false
		}
		wanted = net.JoinHostPort(host, port)
	}
	return u.Host == wanted
}

func selectedTLSReferencesUnique(value any, location []string, group, certificate, key string) bool {
	switch value := value.(type) {
	case string:
		if value != certificate && value != key {
			return true
		}
		return len(location) == 2 && location[0] == group && (location[1] == "tls_certificate_file" && value == certificate || location[1] == "tls_private_key_file" && value == key)
	case map[string]any:
		for name, child := range value {
			if !selectedTLSReferencesUnique(child, append(location, name), group, certificate, key) {
				return false
			}
		}
	case []any:
		for index, child := range value {
			if !selectedTLSReferencesUnique(child, append(location, strconv.Itoa(index)), group, certificate, key) {
				return false
			}
		}
	}
	return true
}

// This pure selector proves the supported literal model/profile only. The caller
// separately verifies actual image/entrypoint, NoCopy mounts, runtime and bytes.
func selectedRestoredTLSProfile(p preparedInputs, model compose.Model, target string) (selectedTLSProfile, error) {
	bad := func() (selectedTLSProfile, error) {
		return selectedTLSProfile{}, failure(selectedTLSUnsupportedProfile)
	}
	if target != "browser" && target != "machine" {
		return bad()
	}
	control, ok := model.Services["control"]
	if !ok || len(control.Environment) != 0 {
		return bad()
	}
	if _, _, e := roleUser(model, "control-config"); e != nil {
		return bad()
	}
	declared, mounted, root := 0, 0, ""
	for _, role := range model.Volumes {
		if role == "control-config" {
			declared++
		}
	}
	for service, value := range model.Services {
		for _, mount := range value.Mounts {
			if mount.Source == "control-config" {
				if service != "control" || mount.Type != "volume" || !mount.ReadOnly || !selectedTLSPath(mount.Target) || !strings.HasPrefix(mount.Target, "/run/projectone/") {
					return bad()
				}
				mounted++
				root = mount.Target
			}
		}
	}
	if declared != 1 || mounted != 1 || !slices.Equal(control.Command, []string{"serve", "--config", root + "/private/runtime.yaml"}) {
		return bad()
	}
	for _, mount := range control.Mounts {
		if mount.Source != "control-config" && (!selectedTLSPath(mount.Target) || selectedTLSOverlaps(root, mount.Target)) {
			return bad()
		}
	}
	for _, tmp := range control.Tmpfs {
		if !selectedTLSPath(tmp.Target) || selectedTLSOverlaps(root, tmp.Target) {
			return bad()
		}
	}
	seen := map[string]bool{}
	for _, file := range p.files["control-config"] {
		if !wire.SafePath(file.Name) || path.Base(file.Name) != file.Name || strings.ContainsAny(file.Name, "\\") || seen[file.Name] || len(file.Contents) == 0 {
			return bad()
		}
		seen[file.Name] = true
	}
	raw, e := restoreNamedFile(p.files["control-config"], "runtime.yaml")
	var document map[string]json.RawMessage
	if e != nil || !utf8.Valid(raw) || wire.UniqueJSON(raw, &document) != nil || document == nil {
		return bad()
	}
	for key := range document {
		if strings.EqualFold(key, "server") && key != "server" || strings.EqualFold(key, "machine_server") && key != "machine_server" {
			return bad()
		}
	}
	server, e := selectedTLSObject(document["server"], []string{"listen_address", "portal_origin", "admin_origin", "tls_certificate_file", "tls_private_key_file", "transport_mode"}, []string{"portal_assets", "admin_assets"})
	if e != nil || server["transport_mode"] != "direct_https" {
		return bad()
	}
	machine, e := selectedTLSObject(document["machine_server"], []string{"listen_address", "origin", "tls_certificate_file", "tls_private_key_file"}, nil)
	if e != nil || !selectedTLSListener(server["listen_address"]) || !selectedTLSListener(machine["listen_address"]) || server["listen_address"] == machine["listen_address"] || server["portal_origin"] == server["admin_origin"] || machine["origin"] == server["portal_origin"] || machine["origin"] == server["admin_origin"] {
		return bad()
	}
	for _, origin := range []string{server["portal_origin"], server["admin_origin"], machine["origin"]} {
		if !selectedTLSOrigin(origin) {
			return bad()
		}
	}
	if _, e = tlsidentity.OriginHosts([]string{server["portal_origin"], server["admin_origin"]}); e != nil {
		return bad()
	}
	if _, e = tlsidentity.MachineOriginHosts(machine["origin"]); e != nil {
		return bad()
	}
	references := map[string]bool{}
	for _, value := range []map[string]string{server, machine} {
		for _, field := range []string{"tls_certificate_file", "tls_private_key_file"} {
			reference := value[field]
			if len(reference) > selectedTLSMaximumReferenceBytes || !selectedTLSPath(reference) || references[reference] || path.Base(reference) == "runtime.yaml" {
				return bad()
			}
			if _, e = restorePayloadAt(p, model, "control-config", reference); e != nil {
				return bad()
			}
			references[reference] = true
		}
	}
	selected, group, origins := server, "server", []string{server["portal_origin"], server["admin_origin"]}
	if target == "machine" {
		selected, group, origins = machine, "machine_server", []string{machine["origin"]}
	}
	var values any
	if wire.UniqueJSON(raw, &values) != nil || !selectedTLSReferencesUnique(values, nil, group, selected["tls_certificate_file"], selected["tls_private_key_file"]) {
		return bad()
	}
	return selectedTLSProfile{CertificateName: path.Base(selected["tls_certificate_file"]), PrivateKeyName: path.Base(selected["tls_private_key_file"]), Origins: origins}, nil
}

func projectSelectedRestoredTLS(p preparedInputs, model compose.Model, target string, certificate, key, trust []byte, now time.Time) ([]engine.ConfigurationFile, tlsidentity.Identity, string, string, error) {
	profile, e := selectedRestoredTLSProfile(p, model, target)
	if e != nil {
		return nil, tlsidentity.Identity{}, "", "", e
	}
	var identity tlsidentity.Identity
	if target == "machine" {
		identity, e = tlsidentity.ValidateMachine(certificate, key, trust, profile.Origins[0], now)
	} else {
		identity, e = tlsidentity.Validate(certificate, key, trust, profile.Origins, now)
	}
	if e != nil {
		return nil, tlsidentity.Identity{}, "", "", e
	}
	previous, e := restoreNamedFile(p.files["control-config"], profile.CertificateName)
	if e != nil || wire.Digest(previous) == identity.CertificateSHA256 {
		return nil, tlsidentity.Identity{}, "", "", failure("OFFLINE_SCOPE_RESTORED_TLS_INPUT_INVALID")
	}
	files := make([]engine.ConfigurationFile, 0, len(p.files["control-config"]))
	for _, file := range p.files["control-config"] {
		contents := file.Contents
		switch file.Name {
		case profile.CertificateName:
			contents = certificate
		case profile.PrivateKeyName:
			contents = key
		}
		files = append(files, engine.ConfigurationFile{Name: file.Name, Contents: slices.Clone(contents)})
	}
	slices.SortFunc(files, func(a, b engine.ConfigurationFile) int { return strings.Compare(a.Name, b.Name) })
	return files, identity, profile.CertificateName, profile.PrivateKeyName, nil
}
