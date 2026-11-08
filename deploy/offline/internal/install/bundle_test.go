package install

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"go.yaml.in/yaml/v3"
)

func TestSignedBundleBindingDoesNotAdoptAnotherRelease(t *testing.T) {
	c := validConfig(t)
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := bundleBinding{FormatVersion: 1, ReleaseID: "test-release", ManifestSHA256: wire.Digest([]byte("fixture-manifest"))}
	for i, role := range bundleImageRoles {
		binding.ImageIDs[i] = "sha256:" + wire.Digest([]byte(role))
	}
	if err := bindRelease(c, binding); err != nil {
		t.Fatal(err)
	}
	if err := bindRelease(c, binding); err != nil {
		t.Fatalf("same release cannot resume: %v", err)
	}
	changed := binding
	changed.ManifestSHA256 = wire.Digest([]byte("different-fixture-manifest"))
	if err := bindRelease(c, changed); err == nil || !strings.Contains(err.Error(), "INSTALL_BUNDLE_UPGRADE_REQUIRED") {
		t.Fatalf("a different manifest bypassed the upgrade path: %v", err)
	}
	bound, err := readBundleBinding(c)
	if err != nil || bound == nil || *bound != binding {
		t.Fatalf("release binding changed: %+v, %v", bound, err)
	}
	if os.Geteuid() == 0 {
		if err := Apply(t.Context(), c, &fakeRunner{}, io.Discard); err == nil || !strings.Contains(err.Error(), "INSTALL_BUNDLE_REQUIRED") {
			t.Fatalf("unsigned apply accepted a bound installation: %v", err)
		}
	}
	c.ComposeFile, c.Images = "", ImagesConfig{}
	config := filepath.Join(shortTempDir(t), "site.yaml")
	raw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	operational, err := LoadOperational(config)
	if err != nil || operational.ComposeFile != c.Layout().ComposeFile || operational.Images != binding.images() {
		t.Fatalf("could not resolve the bound payload: %+v, %v", operational.Images, err)
	}
}

func TestSignedBundleRejectsSiteImageOverride(t *testing.T) {
	c := validConfig(t)
	if err := c.validate(true); err == nil {
		t.Fatal("bundle accepted delivery fields from site configuration")
	}
	c.ComposeFile, c.Images = "", ImagesConfig{}
	if err := c.validate(true); err != nil {
		t.Fatal(err)
	}
}

func TestSignedBundleComposeOnlyUsesVerifiedImages(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "compose", "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(shortTempDir(t), "compose.yaml")
	for _, test := range []struct {
		name, document string
		valid          bool
	}{
		{"repository Compose", string(raw), true},
		{"remote include", string(raw) + "\ninclude: [oci://registry.example.test/compose]\n", false},
		{"remote image", strings.Replace(string(raw), "${OWNSOLID_CONTROL_IMAGE:?}", "registry.example.test/control:1", 1), false},
		{"pull policy", strings.Replace(string(raw), "pull_policy: never", "pull_policy: always", 1), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := checkBundleCompose(path); (err == nil) != test.valid {
				t.Fatalf("valid=%t, got %v", test.valid, err)
			}
		})
	}
}

func TestSignedInstallRefusesChangingTheComposeProject(t *testing.T) {
	c := validConfig(t)
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Layout().ComposeEnv, []byte(c.renderComposeEnv()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkInstalledProject(c); err != nil {
		t.Fatal(err)
	}
	c.Project = "another-installation"
	if err := checkInstalledProject(c); err == nil || !strings.Contains(err.Error(), "INSTALL_BUNDLE_PROJECT_CHANGED") {
		t.Fatalf("accepted a second Compose project on the same data directories: %v", err)
	}
}
