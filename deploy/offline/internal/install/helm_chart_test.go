package install

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const helmTestControlDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestHelmChart(t *testing.T) {
	helm := os.Getenv("OWNSOLID_TEST_HELM")
	if helm == "" {
		t.Skip("set OWNSOLID_TEST_HELM to the local Helm binary")
	}
	chart := os.Getenv("OWNSOLID_TEST_HELM_CHART")
	if chart == "" {
		chart = "../../../helm/ownsolid"
	}
	work := t.TempDir()
	values := filepath.Join(filepath.Dir(chart), "testdata", "site.yaml")
	run := func(t *testing.T, args ...string) ([]byte, error) {
		t.Helper()
		command := exec.CommandContext(t.Context(), helm, args...)
		command.Env = []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + work, "KUBECONFIG=" + os.DevNull,
			"HELM_CACHE_HOME=" + filepath.Join(work, "cache"),
			"HELM_CONFIG_HOME=" + filepath.Join(work, "config"),
			"HELM_DATA_HOME=" + filepath.Join(work, "data"),
			"HELM_NO_PLUGINS=1",
		}
		return command.CombinedOutput()
	}
	common := []string{"--kube-version", "1.36.6", "--namespace", "site-del107", "--values", values}
	if raw, err := run(t, append([]string{"lint", chart, "--strict"}, common...)...); err != nil {
		t.Fatalf("chart lint: %v\n%s", err, raw)
	}
	if raw, err := run(t, "package", chart, "--destination", work); err != nil {
		t.Fatalf("chart package: %v\n%s", err, raw)
	}
	packaged := filepath.Join(work, "ownsolid-0.1.0.tgz")
	args := append([]string{"template", "site", packaged}, common...)
	raw, err := run(t, args...)
	if err != nil {
		t.Fatalf("packaged chart template: %v\n%s", err, raw)
	}
	assertHelmResources(t, raw)
	for _, tc := range []struct {
		name, setting, errorText string
		skipSchema               bool
	}{
		{"missing release digest", "release.manifestSHA256=", "HELM_RELEASE_DIGEST_REQUIRED", true},
		{"missing digest", "images.control=", "/images/control", false},
		{"mutable tag", "images.control=control:stable", "HELM_IMAGE_DIGEST_REQUIRED", true},
		{"malformed digest", "images.control=sha256:abcd", "/images/control", false},
		{"public registry", "registry.address=registry.example.com:5443", "HELM_REGISTRY_ADDRESS_INVALID", true},
		{"invalid port", "registry.address=10.77.107.1:99999", "HELM_REGISTRY_ADDRESS_INVALID", false},
		{"invalid octet", "registry.address=10.77.107.999:5443", "HELM_REGISTRY_ADDRESS_INVALID", false},
		{"inline credential", "smtp.password=unaccepted-field", "smtp", false},
		{"missing TLS Secret", "ingress.tlsSecret=", "/ingress/tlsSecret", false},
		{"missing runtime Secret", "control.runtimeSecret=", "/control/runtimeSecret", false},
		{"duplicate browser names", "ingress.portalHost=admin.site.test", "HELM_INGRESS_HOSTS_INVALID", false},
		{"unsafe SNI", "ingress.adminHost=*.site.test", "/ingress/adminHost", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := []string{"--set-string", tc.setting}
			if tc.skipSchema {
				flags = append(flags, "--skip-schema-validation")
			}
			output, err := run(t, append(args, flags...)...)
			if err == nil || !strings.Contains(string(output), tc.errorText) {
				t.Fatalf("expected refusal %q: %v\n%s", tc.errorText, err, output)
			}
		})
	}
}

func assertHelmResources(t *testing.T, raw []byte) {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	kinds := map[string]bool{"ConfigMap": false, "Deployment": false, "Service": false, "IngressRouteTCP": false}
	for {
		var resource struct {
			Kind     string
			Metadata struct{ Name, Namespace string }
		}
		if err := decoder.Decode(&resource); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		seen, expected := kinds[resource.Kind]
		if !expected || seen || resource.Metadata.Name != "site-control" || resource.Metadata.Namespace != "site-del107" {
			t.Fatalf("unexpected resource %v", resource)
		}
		kinds[resource.Kind] = true
	}
	for kind, seen := range kinds {
		if !seen {
			t.Fatalf("missing %s", kind)
		}
	}
	image := "10.77.107.1:5443/ownsolid/release/control-image@" + helmTestControlDigest
	if strings.Count(string(raw), "image: \""+image+"\"") != 2 {
		t.Fatal("both containers must use the same signed manifest digest")
	}
	for _, expected := range []string{
		"type: Recreate", "automountServiceAccountToken: false", "fsGroupChangePolicy: OnRootMismatch",
		"name: \"site-registry\"", "name: \"control-runtime-v1\"", "name: \"browser-tls-v1\"",
		"claimName: \"models\"", "medium: Memory", "chmod 00700", "install -m 0600", "cd -P \"$input/..data\"",
		"admin_origin: \"https://admin.site.test\"", "portal_origin: \"https://portal.site.test\"",
		"HostSNI(`admin.site.test`) || HostSNI(`portal.site.test`)", "passthrough: true",
	} {
		if !strings.Contains(string(raw), expected) {
			t.Errorf("missing rendered binding %q", expected)
		}
	}
}
