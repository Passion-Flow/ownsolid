package registryconfig

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// Synthetic paths intentionally require no files, credentials, mounts or network.
// Pure decoding must preserve the existing defaults/validation contract.
func probeDecodeConfiguration() Config {
	c := Defaults()
	c.Endpoint, c.AllowedCIDRs = "https://registry.example.invalid:11443", []string{"127.0.0.0/8"}
	c.CAFile, c.CredentialFile = "/synthetic-registry/ca.pem", "/synthetic-registry/credential.json"
	c.StoreRoot, c.PreparedReleaseRoot = "/synthetic-registry/cas", "/synthetic-registry/prepared"
	c.ReleaseArchiveSHA256, c.ReleaseManifestSHA256 = strings.Repeat("a", 64), strings.Repeat("b", 64)
	c.Verification.MountRoot, c.Verification.TrustFile = "/synthetic-registry", "/synthetic-registry/trust.json"
	c.OperationSeconds, c.RequestSeconds, c.GrantSeconds = 73, 31, 49
	c.MaximumPins, c.MaximumReferences, c.MaximumConcurrentImports, c.PageSize = 8, 17, 2, 3
	return c
}

func TestRegistryPureDecodeRetainsStrictConfigurationContract(t *testing.T) {
	expected := probeDecodeConfiguration()
	good, e := json.Marshal(expected)
	if e != nil {
		t.Fatal("synthetic Registry JSON")
	}
	t.Run("valid_nondefault_false_without_files", func(t *testing.T) {
		before := append([]byte{}, good...)
		actual, e := Decode(good)
		if e != nil || !reflect.DeepEqual(actual, expected) || actual.Verification.AllowTestFixture || !bytes.Equal(good, before) {
			t.Fatal("pure typed snapshot changed input or required unavailable runtime files", e)
		}
	})
	t.Run("omitted_fields_retain_owned_defaults", func(t *testing.T) {
		var partial map[string]json.RawMessage
		if json.Unmarshal(good, &partial) != nil {
			t.Fatal("synthetic partial JSON")
		}
		for _, key := range []string{"maximum_archive_bytes", "maximum_expanded_bytes", "maximum_blob_bytes", "maximum_retained_bytes", "minimum_headroom_percent"} {
			delete(partial, key)
		}
		raw, e := json.Marshal(partial)
		if e != nil {
			t.Fatal("synthetic partial encoding")
		}
		actual, e := Decode(raw)
		defaults := Defaults()
		if e != nil || actual.MaximumArchiveBytes != defaults.MaximumArchiveBytes || actual.MaximumRetainedBytes != defaults.MaximumRetainedBytes || actual.MinimumHeadroomPercent != defaults.MinimumHeadroomPercent || actual.RequestSeconds != expected.RequestSeconds {
			t.Fatal("pure decode lost existing partial/default semantics", e)
		}
	})
	t.Run("maximum_document_boundary", func(t *testing.T) {
		raw := append(append([]byte{}, good...), bytes.Repeat([]byte(" "), int(MaximumConfigurationBytes)-len(good))...)
		if actual, e := Decode(raw); e != nil || actual.RequestSeconds != expected.RequestSeconds {
			t.Fatal("inclusive document bound changed", e)
		}
		if actual, e := Decode(append(raw, ' ')); e == nil || !reflect.DeepEqual(actual, Config{}) {
			t.Fatal("oversized pure input accepted or projected partial configuration")
		}
	})
	for name, raw := range map[string][]byte{
		"empty":         nil,
		"null":          []byte(`null`),
		"array":         []byte(`[]`),
		"trailing":      append(append([]byte{}, good...), []byte(` {}`)...),
		"unknown":       append([]byte(`{"unknown_sensitive":"synthetic-sensitive-value",`), good[1:]...),
		"duplicate":     bytes.Replace(good, []byte(`"request_seconds":31`), []byte(`"request_seconds":31,"request_seconds":31`), 1),
		"null_field":    bytes.Replace(good, []byte(`"request_seconds":31`), []byte(`"request_seconds":null`), 1),
		"false_integer": bytes.Replace(good, []byte(`"request_seconds":31`), []byte(`"request_seconds":false`), 1),
		"wrong_type":    bytes.Replace(good, []byte(`"request_seconds":31`), []byte(`"request_seconds":"31"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			actual, e := Decode(raw)
			if e == nil || e.Error() != "REGISTRY_CONFIGURATION_INVALID" || !reflect.DeepEqual(actual, Config{}) || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("strict pure decode accepted or disclosed invalid body", e)
			}
		})
	}
	for name, mutate := range map[string]func(*Config){
		"plaintext_endpoint": func(c *Config) { c.Endpoint = "http://registry.example.invalid" },
		"public_network":     func(c *Config) { c.AllowedCIDRs = []string{"0.0.0.0/0"} },
		"relative_reference": func(c *Config) { c.CredentialFile = "relative-secret-reference" },
		"overlapping_stores": func(c *Config) { c.PreparedReleaseRoot = c.StoreRoot },
		"test_fixture_true":  func(c *Config) { c.Verification.AllowTestFixture = true },
		"request_budget":     func(c *Config) { c.RequestSeconds = c.OperationSeconds + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := expected
			mutate(&candidate)
			raw, e := json.Marshal(candidate)
			if e != nil {
				t.Fatal("synthetic contradiction JSON")
			}
			actual, e := Decode(raw)
			if e == nil || e.Error() != "REGISTRY_CONFIGURATION_INVALID" || !reflect.DeepEqual(actual, Config{}) {
				t.Fatal("pure decode weakened an existing configuration invariant", e)
			}
		})
	}
}

func TestRegistryLegacyLoaderUsesProtectedFactorySnapshot(t *testing.T) {
	expected := probeDecodeConfiguration()
	good, e := json.Marshal(expected)
	if e != nil {
		t.Fatal("synthetic legacy Registry encoding")
	}
	for _, name := range []string{"valid_private", "valid_read_only", "public_mode", "symlink", "hardlink", "fifo", "oversize", "missing", "invalid_body"} {
		t.Run(name, func(t *testing.T) {
			root, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil || os.Chmod(root, 0700) != nil {
				t.Fatal("private legacy Registry fixture root")
			}
			path := filepath.Join(root, "synthetic-registry.json")
			if e := os.WriteFile(path, good, 0600); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "valid_read_only":
				e = os.Chmod(path, 0400)
			case "public_mode":
				e = os.Chmod(path, 0644)
			case "symlink":
				link := filepath.Join(root, "linked.json")
				e = os.Symlink(path, link)
				path = link
			case "hardlink":
				e = os.Link(path, filepath.Join(root, "second-link.json"))
			case "fifo", "missing":
				e = os.Remove(path)
				if e == nil && name == "fifo" {
					e = syscall.Mkfifo(path, 0600)
				}
			case "oversize":
				e = os.WriteFile(path, bytes.Repeat([]byte(" "), int(MaximumConfigurationBytes)+1), 0600)
			case "invalid_body":
				e = os.WriteFile(path, []byte(`{"synthetic_sensitive":"unowned-sensitive-value"}`), 0600)
			}
			if e != nil {
				t.Fatal("legacy Registry fixture setup", e)
			}
			actual, e := Load(path)
			if name == "valid_private" || name == "valid_read_only" {
				if e != nil || !reflect.DeepEqual(actual, expected) || actual.Verification.AllowTestFixture {
					t.Fatal("actual legacy factory reader changed strict nondefault/false Registry configuration", e)
				}
				return
			}
			code := "REGISTRY_CONFIGURATION_REQUIRED"
			if name == "invalid_body" {
				code = "REGISTRY_CONFIGURATION_INVALID"
			}
			if e == nil || e.Error() != code || !reflect.DeepEqual(actual, Config{}) {
				t.Fatal("unsafe legacy Registry snapshot passed or disclosed input", e)
			}
		})
	}
}
