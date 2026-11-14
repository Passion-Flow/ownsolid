//go:build registry_integration

package registryfixture

import (
	"strings"
	"testing"
)

func TestNativeRegistryEnvironmentRejectsSharedOverridesAndSecrets(t *testing.T) {
	for key, value := range map[string]string{"REGISTRY_HTTP_ADDR": "0.0.0.0:1", "REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY": "/unowned", "HTTP_PROXY": "http://unowned.invalid", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://unowned.invalid", "OTEL_TRACES_EXPORTER": "otlp", "PROJECT_ONE_PRIVATE_CREDENTIAL": "explicit-test-only", "GOMEMLIMIT": "9GiB", "GOMAXPROCS": "10"} {
		t.Setenv(key, value)
	}
	seen := map[string]string{}
	for _, entry := range fixtureEnvironment(128) {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || seen[key] != "" {
			t.Fatal("invalid/duplicate child environment")
		}
		seen[key] = value
	}
	if seen["GOMEMLIMIT"] != "128MiB" || seen["GOMAXPROCS"] != "1" || seen["OTEL_TRACES_EXPORTER"] != "none" {
		t.Fatal("native budget/export restriction changed")
	}
	for key := range seen {
		switch key {
		case "GOMAXPROCS", "GOMEMLIMIT", "OTEL_TRACES_EXPORTER", "PATH", "TMPDIR", "TZ":
		default:
			t.Fatal("ambient value inherited", key)
		}
	}
}
