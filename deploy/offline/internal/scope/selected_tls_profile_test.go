package scope

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
)

func selectedTLSFixture(t *testing.T) (preparedInputs, compose.Model) {
	t.Helper()
	p, model := tlsProjectionFixture()
	var document map[string]any
	if json.Unmarshal(p.files["control-config"][0].Contents, &document) != nil {
		t.Fatal("synthetic runtime configuration")
	}
	server := document["server"].(map[string]any)
	server["listen_address"] = "0.0.0.0:8443"
	server["transport_mode"] = "direct_https"
	server["portal_assets"] = "/opt/projectone/portal"
	server["admin_assets"] = "/opt/projectone/admin"
	document["machine_server"] = map[string]any{
		"listen_address": "0.0.0.0:9443", "origin": "https://machine.example.test:19443",
		"tls_certificate_file": "/run/projectone/config/private/machine-certificate.pem",
		"tls_private_key_file": "/run/projectone/config/private/machine-private-key.pem",
	}
	document["licensing"] = nil
	selectedTLSSetRuntime(t, &p, document)
	p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "machine-certificate.pem", Contents: []byte("synthetic previous machine certificate")}, engine.ConfigurationFile{Name: "machine-private-key.pem", Contents: []byte("synthetic previous machine private key")})
	p.files["operator-config"] = []engine.ConfigurationFile{{Name: "control.yaml", Contents: []byte("synthetic separate operator bytes")}}
	control := model.Services["control"]
	control.User = "65532:65532"
	control.Command = []string{"serve", "--config", "/run/projectone/config/private/runtime.yaml"}
	control.ReadOnly = true
	control.Mounts = append(control.Mounts, compose.Mount{Type: "volume", Source: "models", Target: "/var/lib/projectone/models"})
	control.Tmpfs = []compose.Tmpfs{{Target: "/tmp", SizeBytes: 4096, UID: 65532, GID: 65532}}
	model.Services["control"] = control
	model.Volumes = []string{"control-config", "models"}
	return p, model
}

func selectedTLSSetRuntime(t *testing.T, p *preparedInputs, document map[string]any) {
	t.Helper()
	raw, e := json.MarshalIndent(document, "", "  ")
	if e != nil {
		t.Fatal("synthetic runtime encode")
	}
	for index, file := range p.files["control-config"] {
		if file.Name == "runtime.yaml" {
			p.files["control-config"][index].Contents = append(append([]byte(" \n"), raw...), '\n')
			return
		}
	}
	t.Fatal("synthetic runtime inventory")
}

func selectedTLSDocument(t *testing.T, p preparedInputs) map[string]any {
	t.Helper()
	raw, e := restoreNamedFile(p.files["control-config"], "runtime.yaml")
	var document map[string]any
	if e != nil || json.Unmarshal(raw, &document) != nil {
		t.Fatal("synthetic runtime decode")
	}
	return document
}

func selectedTLSCopyFiles(p preparedInputs) map[string][]engine.ConfigurationFile {
	copy := map[string][]engine.ConfigurationFile{}
	for role, files := range p.files {
		for _, file := range files {
			copy[role] = append(copy[role], engine.ConfigurationFile{Name: file.Name, Contents: bytes.Clone(file.Contents)})
		}
	}
	return copy
}

func TestSelectedRestoredTLSProfileRequiresLiteralDualHTTPS(t *testing.T) {
	for _, variant := range []string{"browser", "machine", "same_host_different_ports", "ipv6"} {
		t.Run(variant, func(t *testing.T) {
			p, model := selectedTLSFixture(t)
			target := variant
			document := selectedTLSDocument(t, p)
			server, machine := document["server"].(map[string]any), document["machine_server"].(map[string]any)
			switch variant {
			case "same_host_different_ports":
				target = "browser"
				server["admin_origin"] = "https://portal.example.test:18444"
			case "ipv6":
				target = "machine"
				server["listen_address"], machine["listen_address"] = "[::]:8443", "[::]:9443"
				server["portal_origin"], server["admin_origin"], machine["origin"] = "https://[::1]:18443", "https://[::1]:18444", "https://[::1]:19443"
			}
			selectedTLSSetRuntime(t, &p, document)
			before, beforeModel := selectedTLSCopyFiles(p), encode(model)
			profile, e := selectedRestoredTLSProfile(p, model, target)
			if e != nil {
				t.Fatal("literal profile refused", e)
			}
			wantCertificate, wantKey := "certificate.pem", "private-key.pem"
			wantOrigins := []string{server["portal_origin"].(string), server["admin_origin"].(string)}
			if target == "machine" {
				wantCertificate, wantKey, wantOrigins = "machine-certificate.pem", "machine-private-key.pem", []string{machine["origin"].(string)}
			}
			if profile.CertificateName != wantCertificate || profile.PrivateKeyName != wantKey || !slices.Equal(profile.Origins, wantOrigins) || !reflect.DeepEqual(before, p.files) || !bytes.Equal(beforeModel, encode(model)) {
				t.Fatal("selected identity or immutable input changed")
			}
			profile.Origins[0] = "https://changed.example.test"
			again, e := selectedRestoredTLSProfile(p, model, target)
			if e != nil || !slices.Equal(again.Origins, wantOrigins) {
				t.Fatal("returned origins alias input")
			}
		})
	}
}

func TestSelectedRestoredTLSProfileRejectsAmbiguousInputs(t *testing.T) {
	faults := []string{
		"unknown_target", "missing_control", "invalid_owner", "default_command", "shell_command", "extra_flag", "environment_file", "duplicate_config_flag", "other_config", "combined_config_flag", "model_environment",
		"undeclared_volume", "duplicate_volume", "missing_mount", "writable_mount", "bind_mount", "foreign_peer_mount", "duplicate_mount", "foreign_mount_root", "noncanonical_mount_root", "ancestor_mount", "descendant_mount", "same_target_mount", "ancestor_tmpfs", "descendant_tmpfs", "same_target_tmpfs",
		"missing_server", "missing_machine", "null_server", "null_machine", "partial_server", "partial_machine", "unknown_server_field", "unknown_machine_field", "server_field_case", "machine_field_case", "server_group_case", "machine_group_case", "server_false", "machine_false", "server_null_field", "machine_null_field", "missing_mode", "empty_mode", "http_mode", "proxy_mode", "empty_proxy_list", "empty_listener", "noncanonical_listener", "listener_collision", "http_origin", "origin_case", "default_origin_port", "query_origin", "browser_origin_collision", "machine_origin_collision",
		"foreign_reference", "nested_reference", "traversal_reference", "runtime_reference", "missing_reference_file", "same_pair_reference", "cross_pair_reference", "duplicate_inventory", "empty_inventory_file", "unsafe_inventory_name", "yaml_document", "duplicate_json_key", "trailing_json", "array_document", "null_document", "invalid_utf8",
	}
	for _, fault := range faults {
		t.Run(fault, func(t *testing.T) {
			p, model := selectedTLSFixture(t)
			document := selectedTLSDocument(t, p)
			server, machine := document["server"].(map[string]any), document["machine_server"].(map[string]any)
			control, target := model.Services["control"], "machine"
			switch fault {
			case "unknown_target":
				target = "SYNTHETIC_PRIVATE_TARGET"
			case "missing_control":
				delete(model.Services, "control")
			case "invalid_owner":
				control.User = "0:0"
			case "default_command":
				control.Command = nil
			case "shell_command":
				control.Command = []string{"sh", "-c", "control serve --config /run/projectone/config/private/runtime.yaml"}
			case "extra_flag":
				control.Command = append(control.Command, "--reason", "unexpected")
			case "environment_file":
				control.Command = append(control.Command, "--env-file", "/run/projectone/config/private/policy.env")
			case "duplicate_config_flag":
				control.Command = append(control.Command, "--config", "/run/projectone/config/private/runtime.yaml")
			case "other_config":
				control.Command[2] = "/run/projectone/config/private/control.yaml"
			case "combined_config_flag":
				control.Command = []string{"serve", "--config=/run/projectone/config/private/runtime.yaml"}
			case "model_environment":
				control.Environment = map[string]string{"PROJECT_ONE_MACHINE_ORIGIN": "https://foreign.example.test"}
			case "undeclared_volume":
				model.Volumes = []string{"models"}
			case "duplicate_volume":
				model.Volumes = append(model.Volumes, "control-config")
			case "missing_mount":
				control.Mounts = control.Mounts[1:]
			case "writable_mount":
				control.Mounts[0].ReadOnly = false
			case "bind_mount":
				control.Mounts[0].Type = "bind"
			case "foreign_peer_mount":
				model.Services["postgres"] = compose.Service{Mounts: []compose.Mount{control.Mounts[0]}}
			case "duplicate_mount":
				control.Mounts = append(control.Mounts, control.Mounts[0])
			case "foreign_mount_root":
				control.Mounts[0].Target = "/outside/config"
			case "noncanonical_mount_root":
				control.Mounts[0].Target = "/run/projectone/other/../config"
			case "ancestor_mount", "descendant_mount", "same_target_mount":
				values := map[string]string{"ancestor_mount": "/run", "descendant_mount": "/run/projectone/config/private", "same_target_mount": "/run/projectone/config"}
				control.Mounts = append(control.Mounts, compose.Mount{Type: "volume", Source: "models", Target: values[fault]})
			case "ancestor_tmpfs", "descendant_tmpfs", "same_target_tmpfs":
				values := map[string]string{"ancestor_tmpfs": "/run", "descendant_tmpfs": "/run/projectone/config/private/runtime.yaml", "same_target_tmpfs": "/run/projectone/config"}
				control.Tmpfs = append(control.Tmpfs, compose.Tmpfs{Target: values[fault]})
			case "missing_server":
				delete(document, "server")
			case "missing_machine":
				delete(document, "machine_server")
			case "null_server":
				document["server"] = nil
			case "null_machine":
				document["machine_server"] = nil
			case "partial_server":
				delete(server, "admin_origin")
			case "partial_machine":
				delete(machine, "tls_private_key_file")
			case "unknown_server_field":
				server["extra"] = "unsupported"
			case "unknown_machine_field":
				machine["extra"] = "unsupported"
			case "server_field_case":
				server["PORTAL_ORIGIN"] = server["portal_origin"]
			case "machine_field_case":
				machine["ORIGIN"] = machine["origin"]
			case "server_group_case":
				document["Server"] = map[string]any{}
			case "machine_group_case":
				document["MACHINE_SERVER"] = map[string]any{}
			case "server_false":
				server["portal_origin"] = false
			case "machine_false":
				machine["origin"] = false
			case "server_null_field":
				server["portal_assets"] = nil
			case "machine_null_field":
				machine["tls_private_key_file"] = nil
			case "missing_mode":
				delete(server, "transport_mode")
			case "empty_mode":
				server["transport_mode"] = ""
			case "http_mode":
				server["transport_mode"] = "http"
			case "proxy_mode":
				server["transport_mode"] = "trusted_proxy_https"
			case "empty_proxy_list":
				server["trusted_proxy_cidrs"] = []string{}
			case "empty_listener":
				machine["listen_address"] = ""
			case "noncanonical_listener":
				machine["listen_address"] = "0.0.0.0:09443"
			case "listener_collision":
				machine["listen_address"] = server["listen_address"]
			case "http_origin":
				machine["origin"] = "http://machine.example.test:19443"
			case "origin_case":
				machine["origin"] = "https://Machine.example.test:19443"
			case "default_origin_port":
				machine["origin"] = "https://machine.example.test:443"
			case "query_origin":
				machine["origin"] = "https://machine.example.test:19443?"
			case "browser_origin_collision":
				server["admin_origin"] = server["portal_origin"]
			case "machine_origin_collision":
				machine["origin"] = server["portal_origin"]
			case "foreign_reference":
				machine["tls_private_key_file"] = "/outside/SYNTHETIC_PRIVATE_KEY"
			case "nested_reference":
				machine["tls_private_key_file"] = "/run/projectone/config/private/nested/machine-private-key.pem"
			case "traversal_reference":
				machine["tls_private_key_file"] = "/run/projectone/config/private/../machine-private-key.pem"
			case "runtime_reference":
				machine["tls_private_key_file"] = "/run/projectone/config/private/runtime.yaml"
			case "missing_reference_file":
				p.files["control-config"] = p.files["control-config"][:len(p.files["control-config"])-1]
			case "same_pair_reference":
				machine["tls_private_key_file"] = machine["tls_certificate_file"]
			case "cross_pair_reference":
				machine["tls_certificate_file"] = server["tls_certificate_file"]
			case "duplicate_inventory":
				p.files["control-config"] = append(p.files["control-config"], p.files["control-config"][1])
			case "empty_inventory_file":
				p.files["control-config"][1].Contents = nil
			case "unsafe_inventory_name":
				p.files["control-config"][1].Name = "../runtime-dsn"
			}
			if fault != "missing_control" {
				model.Services["control"] = control
			}
			selectedTLSSetRuntime(t, &p, document)
			switch fault {
			case "yaml_document":
				p.files["control-config"][0].Contents = []byte("server:\n  transport_mode: direct_https\n")
			case "duplicate_json_key":
				p.files["control-config"][0].Contents = bytes.Replace(p.files["control-config"][0].Contents, []byte(`"transport_mode": "direct_https"`), []byte(`"transport_mode":"direct_https","transport_mode":"direct_https"`), 1)
			case "trailing_json":
				p.files["control-config"][0].Contents = append(p.files["control-config"][0].Contents, []byte("{}")...)
			case "array_document":
				p.files["control-config"][0].Contents = []byte("[]")
			case "null_document":
				p.files["control-config"][0].Contents = []byte("null")
			case "invalid_utf8":
				p.files["control-config"][0].Contents = bytes.Replace(p.files["control-config"][0].Contents, []byte("/opt/projectone/portal"), []byte{0xff}, 1)
			}
			profile, e := selectedRestoredTLSProfile(p, model, target)
			if e == nil || e.Error() != selectedTLSUnsupportedProfile || !reflect.DeepEqual(profile, selectedTLSProfile{}) {
				t.Fatal("ambiguous profile not refused with an empty redacted result", e)
			}
		})
	}
}

func TestSelectedRestoredTLSProfileRejectsRecursiveReferenceAliases(t *testing.T) {
	for _, target := range []string{"browser", "machine"} {
		for _, consumer := range []string{"database_dsn", "auth_hmac", "mail_keyring", "sso_keyring", "smtp_ca", "license_trust", "future_array", "future_nested", "certificate_consumer", "selected_group_optional"} {
			t.Run(target+"/"+consumer, func(t *testing.T) {
				p, model := selectedTLSFixture(t)
				document := selectedTLSDocument(t, p)
				group := "server"
				if target == "machine" {
					group = "machine_server"
				}
				reference := document[group].(map[string]any)["tls_private_key_file"]
				switch consumer {
				case "database_dsn":
					document["database"] = map[string]any{"dsn_file": reference}
				case "auth_hmac":
					document["security"] = map[string]any{"auth_hmac_key_file": reference}
				case "mail_keyring":
					document["security"] = map[string]any{"mail_keyring_file": reference}
				case "sso_keyring":
					document["sso"] = map[string]any{"keyring_file": reference}
				case "smtp_ca":
					document["smtp"] = map[string]any{"ca_file": reference}
				case "license_trust":
					document["licensing"] = map[string]any{"trust_file": reference}
				case "future_array":
					document["future_configuration"] = []any{nil, false, []any{reference}}
				case "future_nested":
					document["future_configuration"] = map[string]any{"server": map[string]any{"tls_private_key_file": reference}}
				case "certificate_consumer":
					document["future_configuration"] = map[string]any{"reference": document[group].(map[string]any)["tls_certificate_file"]}
				case "selected_group_optional":
					document["server"].(map[string]any)["portal_assets"] = reference
				}
				selectedTLSSetRuntime(t, &p, document)
				if _, e := selectedRestoredTLSProfile(p, model, target); e == nil || e.Error() != selectedTLSUnsupportedProfile {
					t.Fatal("selected private reference reused by another JSON consumer", e)
				}
			})
		}
	}
}

// Generated CA/key material is synthetic and stays in memory; no trust is installed.
func selectedTLSCertificate(t *testing.T, hosts []string, now time.Time, serial int64) ([]byte, []byte, []byte) {
	t.Helper()
	rootKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(serial * 2), Subject: pkix.Name{CommonName: "Synthetic selected TLS root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(96 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	rootDER, e := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	root, e = x509.ParseCertificate(rootDER)
	if e != nil {
		t.Fatal(e)
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(serial*2 + 1), Subject: pkix.Name{CommonName: "Synthetic selected TLS leaf"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(48 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else {
			leaf.DNSNames = append(leaf.DNSNames, host)
		}
	}
	leafDER, e := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, rootKey)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
}

func TestSelectedRestoredTLSProjectionPreservesUnselectedBytes(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, target := range []string{"browser", "machine"} {
		t.Run(target, func(t *testing.T) {
			p, model := selectedTLSFixture(t)
			before, beforeModel := selectedTLSCopyFiles(p), encode(model)
			profile, e := selectedRestoredTLSProfile(p, model, target)
			if e != nil {
				t.Fatal(e)
			}
			hosts := []string{"portal.example.test", "admin.example.test"}
			if target == "machine" {
				hosts = []string{"machine.example.test"}
			}
			certificate, key, trust := selectedTLSCertificate(t, hosts, now, 1)
			certificateBefore, keyBefore, trustBefore := bytes.Clone(certificate), bytes.Clone(key), bytes.Clone(trust)
			wantHosts := slices.Clone(hosts)
			slices.Sort(wantHosts)
			files, identity, certName, keyName, e := projectSelectedRestoredTLS(p, model, target, certificate, key, trust, now)
			if e != nil || certName != profile.CertificateName || keyName != profile.PrivateKeyName || identity.LeafSHA256 == "" || !slices.Equal(identity.Hosts, wantHosts) {
				t.Fatal("selected projection", e)
			}
			if len(files) != len(p.files["control-config"]) || !slices.IsSortedFunc(files, func(a, b engine.ConfigurationFile) int { return strings.Compare(a.Name, b.Name) }) {
				t.Fatal("full sorted inventory required")
			}
			for _, file := range files {
				want, _ := restoreNamedFile(before["control-config"], file.Name)
				if file.Name == certName {
					want = certificateBefore
				}
				if file.Name == keyName {
					want = keyBefore
				}
				if !bytes.Equal(file.Contents, want) {
					t.Fatal("unselected or runtime bytes changed")
				}
			}
			again, againIdentity, _, _, e := projectSelectedRestoredTLS(p, model, target, certificate, key, trust, now)
			if e != nil || !reflect.DeepEqual(files, again) || !reflect.DeepEqual(identity, againIdentity) {
				t.Fatal("pure projection is not deterministic", e)
			}
			certificate[0] ^= 1
			key[0] ^= 1
			actualCertificate, _ := restoreNamedFile(files, certName)
			actualKey, _ := restoreNamedFile(files, keyName)
			if !bytes.Equal(actualCertificate, certificateBefore) || !bytes.Equal(actualKey, keyBefore) || !bytes.Equal(trust, trustBefore) {
				t.Fatal("result aliases caller material")
			}
			for index := range files {
				files[index].Contents[0] ^= 1
			}
			if !reflect.DeepEqual(before, p.files) || !bytes.Equal(beforeModel, encode(model)) {
				t.Fatal("result aliases immutable input or model")
			}
		})
	}
}

func TestSelectedRestoredTLSProjectionCarriesMachineBrowserMachine(t *testing.T) {
	// This is pure explicit carry-forward, not durable custody or Engine qualification.
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	p, model := selectedTLSFixture(t)
	before, beforeModel := selectedTLSCopyFiles(p), encode(model)
	current := p
	current.files = selectedTLSCopyFiles(p)
	var firstMachine, browser []byte
	for index, target := range []string{"machine", "browser", "machine"} {
		hosts := []string{"machine.example.test"}
		if target == "browser" {
			hosts = []string{"portal.example.test", "admin.example.test"}
		}
		certificate, key, trust := selectedTLSCertificate(t, hosts, now, int64(index+1))
		prior := selectedTLSCopyFiles(current)
		files, _, certName, keyName, e := projectSelectedRestoredTLS(current, model, target, certificate, key, trust, now)
		if e != nil {
			t.Fatal("explicit preceding generation refused", e)
		}
		for _, file := range files {
			if file.Name != certName && file.Name != keyName {
				want, _ := restoreNamedFile(prior["control-config"], file.Name)
				if !bytes.Equal(file.Contents, want) {
					t.Fatal("earlier effective generation reverted")
				}
			}
		}
		if index == 0 {
			firstMachine = bytes.Clone(certificate)
		}
		if index == 1 {
			browser = bytes.Clone(certificate)
			machineBytes, _ := restoreNamedFile(files, "machine-certificate.pem")
			if !bytes.Equal(machineBytes, firstMachine) {
				t.Fatal("browser rotation lost prior machine bytes")
			}
		}
		if index == 2 {
			browserBytes, _ := restoreNamedFile(files, "certificate.pem")
			if !bytes.Equal(browserBytes, browser) || bytes.Equal(certificate, firstMachine) {
				t.Fatal("second machine rotation lost prior browser bytes")
			}
		}
		current.files["control-config"] = files
	}
	if !reflect.DeepEqual(before, p.files) || !bytes.Equal(beforeModel, encode(model)) {
		t.Fatal("pure carry-forward mutated original snapshot")
	}
}

func TestSelectedRestoredTLSProjectionRejectsInvalidMaterial(t *testing.T) {
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	certificate, key, trust := selectedTLSCertificate(t, []string{"machine.example.test"}, now, 1)
	otherCertificate, otherKey, otherTrust := selectedTLSCertificate(t, []string{"other.example.test"}, now, 2)
	expiredCertificate, expiredKey, expiredTrust := selectedTLSCertificate(t, []string{"machine.example.test"}, now.Add(-72*time.Hour), 3)
	for _, fault := range []string{"invalid_certificate", "invalid_key", "untrusted_chain", "wrong_key", "wrong_SAN", "expired", "zero_time", "unchanged_certificate"} {
		t.Run(fault, func(t *testing.T) {
			p, model := selectedTLSFixture(t)
			c, k, ca, at := certificate, key, trust, now
			switch fault {
			case "invalid_certificate":
				c = []byte("SYNTHETIC_PRIVATE_INVALID_CERTIFICATE")
			case "invalid_key":
				k = []byte("SYNTHETIC_PRIVATE_INVALID_KEY")
			case "untrusted_chain":
				ca = otherTrust
			case "wrong_key":
				k = otherKey
			case "wrong_SAN":
				c, k, ca = otherCertificate, otherKey, otherTrust
			case "expired":
				c, k, ca = expiredCertificate, expiredKey, expiredTrust
			case "zero_time":
				at = time.Time{}
			case "unchanged_certificate":
				for index := range p.files["control-config"] {
					if p.files["control-config"][index].Name == "machine-certificate.pem" {
						p.files["control-config"][index].Contents = bytes.Clone(certificate)
					}
				}
			}
			before := selectedTLSCopyFiles(p)
			files, identity, certName, keyName, e := projectSelectedRestoredTLS(p, model, "machine", c, k, ca, at)
			if e == nil || len(files) != 0 || !reflect.DeepEqual(identity, tlsidentity.Identity{}) || certName != "" || keyName != "" || !reflect.DeepEqual(before, p.files) {
				t.Fatal("invalid material returned a projection")
			}
			if e.Error() != "OFFLINE_TLS_SERVING_IDENTITY_INVALID" && e.Error() != "OFFLINE_SCOPE_RESTORED_TLS_INPUT_INVALID" {
				t.Fatal("unexpected material failure code")
			}
		})
	}
}
