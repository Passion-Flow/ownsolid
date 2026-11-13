package install

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func listenerSnapshotHostPath(t *testing.T, c Config, path string) string {
	t.Helper()
	for container, host := range map[string]string{containerConfigDir: c.Layout().Config, containerOperatorDir: c.Layout().Operator} {
		if relative, ok := strings.CutPrefix(path, container+"/"); ok && relative == filepath.Clean(relative) && !strings.HasPrefix(relative, "../") && !filepath.IsAbs(relative) {
			return filepath.Join(host, relative)
		}
	}
	t.Fatalf("unexpected serving reference: %s", path)
	return ""
}

func listenerInstalledFiles(t *testing.T, c Config) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(c.Layout().Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			var err error
			files[path], err = os.ReadFile(path)
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func assertListenerFilesUnchanged(t *testing.T, c Config, before map[string][]byte) {
	t.Helper()
	after := listenerInstalledFiles(t, c)
	if len(before) != len(after) {
		t.Fatal("serving admission left additional installed files")
	}
	for path, raw := range before {
		if !bytes.Equal(raw, after[path]) {
			t.Fatalf("serving admission changed installed file %s", filepath.Base(path))
		}
	}
}

func sharedListenerFixture(t *testing.T) Config {
	t.Helper()
	c := portalConfig(t)
	portal := c.Network.PortalListener
	c.Network.PortalListener = nil
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	c.Network.PortalListener = portal
	return c
}

func replacementListenerTLS(t *testing.T, c *Config) *servingTLSMaterial {
	t.Helper()
	authority, authorityKey, err := generateCA("synthetic replacement CA")
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := c.originHosts()
	if err != nil {
		t.Fatal(err)
	}
	certificate, key, err := issueServerCertificate(authority, authorityKey, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLS.Source != "files" {
		directory := shortTempDir(t)
		c.TLS = TLSConfig{Source: "files", CertificateFile: filepath.Join(directory, "certificate.pem"), PrivateKeyFile: filepath.Join(directory, "private-key.pem"), CAFile: filepath.Join(directory, "ca.pem")}
	}
	material := &servingTLSMaterial{certificate: certificate, privateKey: key, authority: authority}
	for path, raw := range map[string][]byte{c.TLS.CertificateFile: certificate, c.TLS.PrivateKeyFile: key, c.TLS.CAFile: authority} {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return material
}

func listenerMaterialRunner(t *testing.T, c Config, inspect func(int, []byte, []byte)) (Runner, *int, *int) {
	t.Helper()
	checks, stops := 0, 0
	runner := listenerRunner(func(_ context.Context, name string, args ...string) (string, error) {
		line := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(line, "ps --all --quiet control"):
			return strings.Repeat("a", 64), nil
		case strings.Contains(line, "inspect --type container"):
			raw, _ := json.Marshal(map[string]string{"Image": "sha256:" + strings.Repeat("b", 64), "Project": c.Project, "Service": "control", "ConfigFile": c.Layout().ComposeFile})
			return string(raw), nil
		case strings.Contains(line, "verify-serving-transport"):
			checks++
			var snapshotPath string
			var expected servingExpectation
			for n, arg := range args {
				switch arg {
				case "--config":
					snapshotPath = listenerSnapshotHostPath(t, c, args[n+1])
				case "--expected-server-json":
					if err := json.Unmarshal([]byte(args[n+1]), &expected); err != nil {
						t.Fatal(err)
					}
				}
			}
			var snapshot struct {
				Server struct {
					Certificate string `yaml:"tls_certificate_file"`
					Key         string `yaml:"tls_private_key_file"`
				} `yaml:"server"`
			}
			raw, err := os.ReadFile(snapshotPath)
			if err != nil || yaml.Unmarshal(raw, &snapshot) != nil {
				t.Fatal("checker could not read the desired snapshot")
			}
			if snapshot.Server.Certificate != expected.TLSCertificateFile || snapshot.Server.Key != expected.TLSPrivateKeyFile {
				t.Fatal("public TLS intent differs from the desired snapshot")
			}
			certificatePath, keyPath := listenerSnapshotHostPath(t, c, snapshot.Server.Certificate), listenerSnapshotHostPath(t, c, snapshot.Server.Key)
			for _, path := range []string{snapshotPath, certificatePath, keyPath} {
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
					t.Fatal("checker input is not a private regular file")
				}
				info, err = os.Lstat(filepath.Dir(path))
				if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
					t.Fatal("checker input directory is not private")
				}
			}
			certificate, err := os.ReadFile(certificatePath)
			if err != nil {
				t.Fatal(err)
			}
			key, err := os.ReadFile(keyPath)
			if err != nil {
				t.Fatal(err)
			}
			// This fixture observes bytes and simulates pair refusal. Actual Control
			// policy and its installed command require their independent qualification.
			if _, err := tls.X509KeyPair(certificate, key); err != nil {
				return "SERVING_TRANSPORT_CONFIGURATION_INVALID\n", errors.New("synthetic checker pair refusal")
			}
			if inspect != nil {
				inspect(checks, certificate, key)
			}
			return servingTransportSuccess + "\n", nil
		case strings.Contains(line, "stop control"):
			stops++
		}
		return successOutput(line), nil
	})
	return runner, &checks, &stops
}

func TestPortalTLSAdmissionRetainsSelectedBytesAcrossApplyAndRetry(t *testing.T) {
	c := sharedListenerFixture(t)
	selected := replacementListenerTLS(t, &c)
	before := listenerInstalledFiles(t, c)
	first := true
	runner, checks, stops := listenerMaterialRunner(t, c, func(_ int, certificate, key []byte) {
		if !bytes.Equal(certificate, selected.certificate) || !bytes.Equal(key, selected.privateKey) {
			t.Fatal("checker received a different TLS pair")
		}
		if first {
			first = false
			// Only the owned temporary subtree may differ before admission.
			for path, original := range before {
				raw, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, original) {
					t.Fatal("source check replaced installed bytes")
				}
			}
			for _, path := range []string{c.TLS.CertificateFile, c.TLS.PrivateKeyFile, c.TLS.CAFile} {
				if err := os.WriteFile(path, []byte("synthetic changed input\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	var output bytes.Buffer
	instance := testInstaller(c, runner, &output)
	if err := instance.apply(t.Context()); err != nil || *checks != 2 || *stops != 1 {
		t.Fatalf("admitted bytes were not retained through Apply: checks=%d stops=%d %v", *checks, *stops, err)
	}
	for name, expected := range map[string][]byte{"certificate.pem": selected.certificate, "private-key.pem": selected.privateKey, "ca.pem": selected.authority} {
		actual, err := os.ReadFile(filepath.Join(c.Layout().Config, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("Materialize substituted selected %s: %v", name, err)
		}
	}
	before = listenerInstalledFiles(t, c)
	if err := instance.apply(t.Context()); err == nil || !strings.Contains(err.Error(), "listener-compatibility") || *checks != 3 || *stops != 1 {
		t.Fatalf("invalid replacement crossed admission: checks=%d stops=%d %v", *checks, *stops, err)
	}
	assertListenerFilesUnchanged(t, c, before)
	selected = replacementListenerTLS(t, &c)
	for attempt := 0; attempt < 2; attempt++ {
		if err := instance.apply(t.Context()); err != nil {
			t.Fatalf("repair/repeat %d failed: %v", attempt, err)
		}
	}
	if *checks != 7 || *stops != 3 {
		t.Fatalf("repair bypassed either checker: checks=%d stops=%d", *checks, *stops)
	}
	for _, pattern := range []string{"serving-intent-*", "serving-image-*"} {
		paths, err := filepath.Glob(filepath.Join(c.Layout().Operator, pattern))
		if err != nil || len(paths) != 0 {
			t.Fatal("serving inputs remain after retry")
		}
	}
}

func TestPortalTLSAdmissionRepairsGeneratedMaterialWithoutEarlyWrites(t *testing.T) {
	for _, fault := range []string{"mismatched-key", "renewal-window"} {
		t.Run(fault, func(t *testing.T) {
			c := sharedListenerFixture(t)
			l := c.Layout()
			authority, err := os.ReadFile(filepath.Join(l.Config, "ca.pem"))
			if err != nil {
				t.Fatal(err)
			}
			authorityKey, err := os.ReadFile(filepath.Join(l.Secrets, "ca-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			if fault == "mismatched-key" {
				_, key, err := generateCA("synthetic unrelated key")
				if err != nil || os.WriteFile(filepath.Join(l.Config, "private-key.pem"), key, 0o600) != nil {
					t.Fatal("cannot prepare mismatched serving pair")
				}
			} else {
				pair, err := tls.LoadX509KeyPair(filepath.Join(l.Config, "certificate.pem"), filepath.Join(l.Config, "private-key.pem"))
				if err != nil {
					t.Fatal(err)
				}
				leaf, err := x509.ParseCertificate(pair.Certificate[0])
				if err != nil {
					t.Fatal(err)
				}
				issuer, issuerKey, err := parseAuthority(authority, authorityKey)
				if err != nil {
					t.Fatal(err)
				}
				leaf.NotAfter = time.Now().Add(24 * time.Hour)
				der, err := x509.CreateCertificate(rand.Reader, leaf, issuer, pair.PrivateKey.(crypto.Signer).Public(), issuerKey)
				if err != nil || os.WriteFile(filepath.Join(l.Config, "certificate.pem"), encodeCertificate(der), 0o600) != nil {
					t.Fatal("cannot prepare expiring serving pair")
				}
			}
			before := listenerInstalledFiles(t, c)
			var selectedCertificate, selectedKey []byte
			runner, _, _ := listenerMaterialRunner(t, c, func(_ int, certificate, key []byte) {
				selectedCertificate, selectedKey = certificate, key
				for path, original := range before {
					raw, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(raw, original) {
						t.Fatal("generated repair changed installed bytes before admission")
					}
				}
			})
			instance := &installer{config: c, runner: runner}
			if err := instance.listenerCompatibility(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertListenerFilesUnchanged(t, c, before)
			hosts, _ := c.originHosts()
			if !serverCertificateCurrent(selectedCertificate, authority, hosts) {
				t.Fatal("generated repair did not retain the original serving authority")
			}
			if err := materialize(c, instance.admittedTLS); err != nil {
				t.Fatal(err)
			}
			for path, expected := range map[string][]byte{filepath.Join(l.Config, "certificate.pem"): selectedCertificate, filepath.Join(l.Config, "private-key.pem"): selectedKey, filepath.Join(l.Config, "ca.pem"): authority, filepath.Join(l.Secrets, "ca-key.pem"): authorityKey} {
				actual, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(actual, expected) {
					t.Fatal("generated publication differs from the admitted material")
				}
			}
			before = listenerInstalledFiles(t, c)
			if err := Materialize(c); err != nil {
				t.Fatal(err)
			}
			assertListenerFilesUnchanged(t, c, before)
		})
	}
}

func TestPortalTLSAdmissionRefusesUnreadableInputBeforeStaging(t *testing.T) {
	c := sharedListenerFixture(t)
	selected := replacementListenerTLS(t, &c)
	if err := os.Remove(c.TLS.PrivateKeyFile); err != nil {
		t.Fatal(err)
	}
	before := listenerInstalledFiles(t, c)
	runner, checks, _ := listenerMaterialRunner(t, c, nil)
	instance := &installer{config: c, runner: runner}
	if err := instance.listenerCompatibility(t.Context()); err == nil || !strings.HasPrefix(err.Error(), "INSTALL_INPUT_UNREADABLE") || *checks != 0 {
		t.Fatalf("unreadable material reached the checker: %v", err)
	}
	assertListenerFilesUnchanged(t, c, before)
	if err := os.WriteFile(c.TLS.PrivateKeyFile, selected.privateKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := instance.listenerCompatibility(t.Context()); err != nil || *checks != 1 {
		t.Fatalf("input repair did not allow explicit retry: %v", err)
	}
	assertListenerFilesUnchanged(t, c, before)
}

func TestPortalTLSCleanupFailurePreservesThePrimaryRefusal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the directory write-permission failure; run as an unprivileged UID")
	}
	parent := shortTempDir(t)
	directory := filepath.Join(parent, "owned")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(parent, 0o700)
		_ = os.RemoveAll(directory)
	})
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	var outcome error = errors.New("SYNTHETIC_SOURCE_REFUSAL")
	removeServingDirectory(directory, &outcome)
	if outcome == nil || !strings.Contains(outcome.Error(), "INSTALL_SERVING_CHECK_CLEANUP_FAILED") || !strings.Contains(outcome.Error(), "SYNTHETIC_SOURCE_REFUSAL") {
		t.Fatal("cleanup failure hid the original refusal")
	}
}

func TestMaterializeTLSReadsRepeatedSourceReferences(t *testing.T) {
	c := sharedListenerFixture(t)
	selected := replacementListenerTLS(t, &c)
	combined := append(append([]byte{}, selected.certificate...), selected.privateKey...)
	if err := os.WriteFile(c.TLS.CertificateFile, combined, 0o600); err != nil {
		t.Fatal(err)
	}
	c.TLS.PrivateKeyFile = c.TLS.CertificateFile
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"certificate.pem", "private-key.pem"} {
		raw, err := os.ReadFile(filepath.Join(c.Layout().Config, name))
		if err != nil || !bytes.Equal(raw, combined) {
			t.Fatalf("repeated reference omitted %s: %v", name, err)
		}
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(c.Layout().Config, "certificate.pem"), filepath.Join(c.Layout().Config, "private-key.pem")); err != nil {
		t.Fatal(err)
	}
}
