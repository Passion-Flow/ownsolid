package install

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func historyControlFixture(t *testing.T) (Config, string) {
	t.Helper()
	path, history := historyFixture(t)
	policy := map[string]any{"format_version": 1, "fleet_history": historyPolicy{
		RawOrigin: "https://10.20.30.40:18428", LongOrigin: "https://10.20.30.40:28428",
		RawHorizonSeconds: 3600, MaxRangeSeconds: 86400, SettlingSeconds: 60,
	}}
	historyWrite(t, history.MonitoringFile, historyJSON(policy))
	// Materialize reads these protected deployment outputs, independently of Docker.
	source := filepath.Join(filepath.Dir(path), "control")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{"monitoring.json": history.MonitoringFile, "ca.pem": history.ControlCAFile} {
		raw, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		historyWrite(t, filepath.Join(source, name), raw)
	}
	c := validConfig(t)
	c.Telemetry.FleetHistoryControlDirectory = source
	return c, source
}

func historyWrite(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFleetHistoryControlMaterializesProtectedPolicyAndTrust(t *testing.T) {
	c, source := historyControlFixture(t)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"monitoring.json", "ca.pem"} {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(c.Layout().Config, "fleet-history", name)
		got, err := os.ReadFile(target)
		st, statErr := os.Stat(target)
		if err != nil || statErr != nil || !bytes.Equal(got, want) || st.Mode().Perm() != 0600 {
			t.Fatalf("protected %s was not preserved: %v %v", name, err, statErr)
		}
	}
	for _, serving := range []bool{false, true} {
		raw, err := c.renderControl("/run/fixture-dsn", serving)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		_, present := document["monitoring_config_file"]
		if present != serving {
			t.Fatalf("monitoring policy present=%v for serving=%v", present, serving)
		}
	}
	if got := c.ComposeVariables()["OWNSOLID_CONTROL_CA_FILE"]; got != containerConfigDir+"/fleet-history/ca.pem" {
		t.Fatal("Control does not select the protected trust file", got)
	}
	// Reapplying a new generation must replace, rather than append, removed trust.
	ca, _, err := generateCA("replacement-control-trust")
	if err != nil {
		t.Fatal(err)
	}
	historyWrite(t, filepath.Join(source, "ca.pem"), ca)
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(c.Layout().Config, "fleet-history", "ca.pem"))
	if err != nil || !bytes.Equal(got, ca) {
		t.Fatal("removed issuer survived materialization", err)
	}
	if got := validConfig(t).ComposeVariables()["OWNSOLID_CONTROL_CA_FILE"]; got != "/etc/ssl/certs/ca-certificates.crt" {
		t.Fatal("legacy installation trust changed", got)
	}
}

func TestFleetHistoryControlRefusesInvalidSourcesBeforeWriting(t *testing.T) {
	for _, fault := range []string{"missing", "writable", "symlink", "invalid-policy", "loopback", "invalid-ca", "duplicate-owner"} {
		t.Run(fault, func(t *testing.T) {
			c, source := historyControlFixture(t)
			policy := filepath.Join(source, "monitoring.json")
			switch fault {
			case "missing":
				if err := os.Remove(policy); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(policy, 0660); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(policy, policy+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(policy+".original", policy); err != nil {
					t.Fatal(err)
				}
			case "invalid-policy":
				historyWrite(t, policy, []byte(`{"format_version":1,"fleet_history":{}}`))
			case "loopback":
				raw, err := os.ReadFile(policy)
				if err != nil {
					t.Fatal(err)
				}
				historyWrite(t, policy, []byte(strings.ReplaceAll(string(raw), "10.20.30.40", "127.0.0.1")))
			case "invalid-ca":
				historyWrite(t, filepath.Join(source, "ca.pem"), []byte("invalid"))
			case "duplicate-owner":
				c.ControlSettings = map[string]any{"monitoring_config_file": "/another/monitoring.json"}
				if err := c.Validate(); err == nil {
					t.Fatal("ambiguous policy owner admitted")
				}
				return
			}
			if err := Materialize(c); err == nil {
				t.Fatal("invalid source was admitted")
			}
			if _, err := os.Stat(c.InstallRoot); !os.IsNotExist(err) {
				t.Fatal("refusal modified the installation", err)
			}
		})
	}
}

func TestFleetHistoryControlRefusesLocalOnlyCoordinatedBackup(t *testing.T) {
	c, _ := historyControlFixture(t)
	if err := checkCoordinatedProfile(c); !errors.Is(err, errBackupScope) {
		t.Fatal("external history state was admitted into a local-only backup", err)
	}
}
