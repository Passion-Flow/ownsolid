package install

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

type helmBundlePreflightInputs struct {
	Installer          string `json:"installer"`
	Helm               string `json:"helm"`
	Archive            string `json:"archive"`
	Verification       string `json:"verification"`
	ArchiveSHA256      string `json:"archive_sha256"`
	ManifestSHA256     string `json:"manifest_sha256"`
	ChartAsset         string `json:"chart_asset"`
	RegistryConfig     string `json:"registry_config"`
	SiteValues         string `json:"site_values"`
	BrowserCA          string `json:"browser_ca"`
	BrowserCertificate string `json:"browser_certificate"`
	BrowserKey         string `json:"browser_key"`
}

// TestSignedHelmBundlePreflight checks one reusable signed bundle in an
// isolated Linux namespace, using the actual installer and pinned Helm CLI.
func TestSignedHelmBundlePreflight(t *testing.T) {
	input := os.Getenv("OWNSOLID_SIGNED_HELM_PREFLIGHT")
	if input == "" {
		t.Skip("requires the staged signed bundle described in deploy/offline/README.md")
	}
	var c helmBundlePreflightInputs
	read := func(path string) []byte {
		t.Helper()
		raw, err := bundle.ReadProtectedConfiguration(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if wire.StrictJSON(read(input), &c) != nil || !wire.ValidSHA256(c.ArchiveSHA256) ||
		!wire.ValidSHA256(c.ManifestSHA256) || c.ChartAsset == "" {
		t.Fatal("a protected fixture with independent release digests is required")
	}
	policy, err := LoadBundleVerification(c.Verification)
	if err != nil || !policy.RequireRKE2Components {
		t.Fatal("the fixture requires the strict RKE2 component policy", err)
	}
	registry, err := loadRegistrySite(c.RegistryConfig)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Setenv("KUBECONFIG", "/dev/null")
	t.Setenv("HELM_NO_PLUGINS", "1")
	t.Setenv("HELM_CACHE_HOME", filepath.Join(work, "helm-cache"))
	t.Setenv("HELM_CONFIG_HOME", filepath.Join(work, "helm-config"))
	t.Setenv("HELM_DATA_HOME", filepath.Join(work, "helm-data"))
	run := func(path string, args ...string) []byte {
		t.Helper()
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			t.Fatal("fixture tool path must be absolute and clean")
		}
		command := exec.CommandContext(t.Context(), path, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s failed: %v: %s", filepath.Base(path), args[0], err, output)
		}
		return output
	}
	if version := strings.TrimSpace(string(run(c.Helm, "version", "--short"))); !strings.HasPrefix(version, "v4.3.1+") {
		t.Fatalf("wrong Helm tool version: %q", version)
	}
	prepared := filepath.Join(work, "prepared")
	p := run(c.Installer, "prepare-bundle", "--bundle", c.Archive, "--verification", c.Verification, "--output", prepared)
	var result bundle.Result
	if wire.StrictJSON(p, &result) != nil || result.Status != "prepared_verified_inventory_only_not_installed" ||
		result.ArchiveSHA256 != c.ArchiveSHA256 || result.ManifestSHA256 != c.ManifestSHA256 {
		t.Fatal("prepare did not admit the expected archive and manifest")
	}
	var manifest wire.Manifest
	raw := read(filepath.Join(prepared, "manifest.json"))
	if wire.Digest(raw) != c.ManifestSHA256 || wire.StrictJSON(raw, &manifest) != nil ||
		manifest.Profile != bundle.CurrentProfile || !slices.Contains(manifest.Prerequisites, bundle.RegistryPrerequisite) {
		t.Fatal("prepared manifest did not bind the signed Registry group")
	}
	assets := make(map[string]wire.Asset, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		assets[asset.ID] = asset
	}
	control := assets["control-image"]
	chart := assets[c.ChartAsset]
	if chart.Kind != "chart" || !wire.SafePath(chart.Path) || assets[bundle.RegistryImageAsset].Kind != "image_archive" {
		t.Fatal("manifest is missing the selected Chart or Registry image")
	}
	chartPath := filepath.Join(prepared, filepath.FromSlash(chart.Path))
	chartBytes := read(chartPath)
	if int64(len(chartBytes)) != chart.Size || wire.Digest(chartBytes) != chart.SHA256 {
		t.Fatal("prepared chart bytes differ from the signed asset")
	}

	values := run(c.Installer, "helm-images", "--bundle", c.Archive, "--verification", c.Verification, "--registry", registry.addr.String())
	var overlay struct {
		Registry struct{ Address string }        `json:"registry"`
		Images   struct{ Control string }        `json:"images"`
		Release  struct{ ManifestSHA256 string } `json:"release"`
	}
	if wire.StrictJSON(values, &overlay) != nil || overlay.Registry.Address != registry.addr.String() ||
		overlay.Images.Control != control.ImageID || overlay.Images.Control == control.ImageConfigDigest ||
		overlay.Release.ManifestSHA256 != c.ManifestSHA256 {
		t.Fatal("helm-images did not return the signed OCI manifest reference and release lock")
	}
	valuePath := filepath.Join(work, "image-values.json")
	if err := os.WriteFile(valuePath, values, 0600); err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Ingress struct {
			Admin  string `yaml:"adminHost"`
			Portal string `yaml:"portalHost"`
		}
		Control struct {
			Claim string `yaml:"modelClaim"`
		}
		Registry struct {
			PullSecret string `yaml:"pullSecret"`
		}
	}
	if err := yaml.Unmarshal(read(c.SiteValues), &expected); err != nil || expected.Control.Claim == "" || expected.Registry.PullSecret == "" {
		t.Fatal("fixture values need explicit site endpoints, claim and pull secret", err)
	}
	pair, err := tls.X509KeyPair(read(c.BrowserCertificate), read(c.BrowserKey))
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(read(c.BrowserCA)) {
		t.Fatal("browser certificate, key or CA is invalid", err)
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		intermediates.AddCert(cert)
	}
	for _, host := range []string{expected.Ingress.Admin, expected.Ingress.Portal} {
		if _, err := pair.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: roots, Intermediates: intermediates}); err != nil {
			t.Fatal("private browser CA cannot verify a rendered hostname", err)
		}
	}

	args := []string{"--values", c.SiteValues, "--values", valuePath}
	run(c.Helm, append([]string{"lint", chartPath}, args...)...)
	rendered := run(c.Helm, append([]string{"template", "site", chartPath, "--kube-version", "1.36.6", "--namespace", "site-del107"}, args...)...)
	image := registry.addr.String() + "/ownsolid/release/control-image@" + control.ImageID
	checkSignedHelmRender(t, rendered, image, c.ManifestSHA256, expected.Ingress.Admin, expected.Ingress.Portal, expected.Control.Claim, expected.Registry.PullSecret)
	t.Logf("archive %s, manifest %s, chart %s", c.ArchiveSHA256, c.ManifestSHA256, chart.SHA256)
	t.Logf("strict RKE2 components and Registry CA accepted; both containers use %s", image)
	t.Logf("private browser CA verifies %s and %s; existing model claim %s", expected.Ingress.Admin, expected.Ingress.Portal, expected.Control.Claim)
}

func checkSignedHelmRender(t *testing.T, rendered []byte, image, manifest, admin, portal, claim, pullSecret string) {
	t.Helper()
	type name struct{ Name string }
	type modelMount struct {
		Name string
		Path string `yaml:"mountPath"`
		Sub  string `yaml:"subPath"`
	}
	type container struct {
		Image  string
		Mounts []modelMount `yaml:"volumeMounts"`
	}
	var foundConfig, foundIngress, foundDeployment bool
	decoder := yaml.NewDecoder(bytes.NewReader(rendered))
	for {
		var doc struct {
			Kind string
			Data map[string]string
			Spec struct {
				Routes   []struct{ Match string }
				TLS      struct{ Passthrough bool }
				Strategy struct{ Type string }
				Template struct {
					Metadata struct{ Annotations map[string]string }
					Spec     struct {
						Init             []container `yaml:"initContainers"`
						Containers       []container
						ImagePullSecrets []name `yaml:"imagePullSecrets"`
						Volumes          []struct {
							Name  string
							Claim struct {
								Name string `yaml:"claimName"`
							} `yaml:"persistentVolumeClaim"`
						}
					}
				}
			}
		}
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch doc.Kind {
		case "ConfigMap":
			var config struct {
				Server struct {
					Admin     string `yaml:"admin_origin"`
					Portal    string `yaml:"portal_origin"`
					Transport string `yaml:"transport_mode"`
				}
			}
			if yaml.Unmarshal([]byte(doc.Data["control.yaml"]), &config) != nil ||
				config.Server.Admin != (&url.URL{Scheme: "https", Host: admin}).String() ||
				config.Server.Portal != (&url.URL{Scheme: "https", Host: portal}).String() ||
				config.Server.Transport != "direct_https" {
				t.Fatal("render did not configure the two direct HTTPS origins")
			}
			foundConfig = true
		case "IngressRouteTCP":
			routes := doc.Spec.Routes
			if !doc.Spec.TLS.Passthrough || len(routes) != 1 ||
				routes[0].Match != "HostSNI(`"+admin+"`) || HostSNI(`"+portal+"`)" || admin == portal {
				t.Fatal("render did not route the exact distinct SNI names with passthrough")
			}
			foundIngress = true
		case "Deployment":
			pod := doc.Spec.Template.Spec
			if doc.Spec.Template.Metadata.Annotations["ownsolid.dev/release-manifest-sha256"] != manifest ||
				doc.Spec.Strategy.Type != "Recreate" ||
				len(pod.Init) != 1 || len(pod.Containers) != 1 || pod.Init[0].Image != image || pod.Containers[0].Image != image ||
				len(pod.ImagePullSecrets) != 1 || pod.ImagePullSecrets[0].Name != pullSecret ||
				!slices.Contains(pod.Init[0].Mounts, modelMount{"models", "/models", "models"}) ||
				!slices.Contains(pod.Containers[0].Mounts, modelMount{"models", "/var/lib/ownsolid/models", "models"}) {
				t.Fatal("render did not bind the release, exact image, scoped pull secret and shared model mount")
			}
			for _, volume := range pod.Volumes {
				if volume.Name == "models" && volume.Claim.Name == claim {
					foundDeployment = true
				}
			}
		}
	}
	if !foundConfig || !foundIngress || !foundDeployment {
		t.Fatal("render is missing the site configuration, ingress or model claim")
	}
}
