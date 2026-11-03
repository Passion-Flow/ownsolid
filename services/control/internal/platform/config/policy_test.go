package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimePolicyDefaultsOverridesAndInvalidConfiguration(t *testing.T) {
	example, err := os.ReadFile("../../../config/examples/control/control.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Split(string(example), "\nruntime:")[0]
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "control.yaml")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, err := Load(write(base))
	if err != nil {
		t.Fatal(err)
	}
	if old.Policy() != DefaultRuntimePolicy() {
		t.Fatal("legacy configuration changed defaults")
	}
	custom, err := Load(write(base + "\nruntime:\n  identity:\n    invitation_ttl_seconds: 7200\n  http:\n    max_connections: 32\n    max_active_requests: 8\n"))
	if err != nil {
		t.Fatal(err)
	}
	if custom.Policy().Identity.InvitationTTLSeconds != 7200 || custom.Policy().HTTP.MaxConnections != 32 || custom.Policy().Identity.ResetTTLSeconds != 900 {
		t.Fatal("partial configuration did not preserve omitted defaults")
	}
	for _, value := range []string{
		"http:\n    max_connections: 0", "http:\n    max_active_requests: 129",
		"identity:\n    admin_idle_seconds: 20000", "identity:\n    session_touch_seconds: 900",
		"identity:\n    reset_minimum_milliseconds: 0", "identity:\n    unknown_secret: synthetic-sensitive-value",
		"mail:\n    max_attempts: 6", "mail:\n    lease_seconds: 20", "mail:\n    attempt_seconds: 15",
		"mail:\n    retry_1_seconds: 600", "nodes:\n    offline_seconds: 25", "ui:\n    poll_milliseconds: 0",
		"retention:\n    batch_size: -1", "rates:\n    reset_cooldown_seconds: 1000",
		"distribution:\n    key_ttl_seconds: 7776001", "distribution:\n    key_max_ttl_seconds: 60",
		"distribution:\n    max_concurrent_requests: 5", "distribution:\n    context_tokens: 2049",
		"distribution:\n    output_tokens: 513", "distribution:\n    alias_max_active: 17",
	} {
		t.Run(strings.ReplaceAll(value, "\n", " "), func(t *testing.T) {
			_, err := Load(write(base + "\nruntime:\n  " + value + "\n"))
			if err == nil {
				t.Fatal("invalid policy accepted")
			}
			if strings.Contains(err.Error(), "synthetic-sensitive-value") {
				t.Fatal("secret leaked")
			}
		})
	}
	complete, err := Load(write(string(example)))
	if err != nil {
		t.Fatal(err)
	}
	if complete.Policy() != DefaultRuntimePolicy() {
		t.Fatal("example drifted from defaults")
	}
}
