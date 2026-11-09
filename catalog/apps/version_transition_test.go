package apps

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

const testTransitionBudget = 1 << 20

type catalogReleaseFixture struct {
	version, engine, before, variant string
	composeFields                    string
	predecessors                     []ReleasePin
}

func transitionCatalog(t *testing.T) (*os.Root, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	key, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return root, key, private
}

func writeTransitionRelease(t *testing.T, catalog *os.Root, key ed25519.PrivateKey, fixture catalogReleaseFixture) ReleasePin {
	t.Helper()
	if err := catalog.Mkdir(fixture.version, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	root, err := catalog.OpenRoot(fixture.version)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("locales", 0700); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	image := imageVariant(t, "amd64", false, fixture.version+fixture.variant, false)
	archive := ociTar(t, image.files, "")
	artifact := engineFixture(t, fixture.engine, fixture.version, image.image)
	if fixture.engine == "compose" {
		artifact = append(artifact, fixture.composeFields...)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(fixtureManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Version = fixture.version
	manifest.Platform.Minimum = "1.0.0"
	manifest.Artifact = Artifact{Engine: fixture.engine, SHA256: digestBytes(artifact)}
	manifest.Images = []string{image.image}
	manifest.UpgradeFrom = fixture.predecessors
	if fixture.before != "" {
		manifest.Platform.Before = fixture.before
	}
	raw := ociJSON(t, manifest)
	for path, data := range map[string][]byte{
		manifestName: raw, englishName: []byte(fixtureEnglish), chineseName: []byte(fixtureChinese),
		manifest.Artifact.Filename(): artifact, ImageArchiveName: archive,
	} {
		if err := root.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	index := ociJSON(t, bundleIndex{
		SchemaVersion: 1, Manifest: digestBytes(raw), English: digestBytes([]byte(fixtureEnglish)),
		Chinese: digestBytes([]byte(fixtureChinese)), Images: digestBytes(archive),
	})
	signFixtureIndex(t, root, key, index)
	return ReleasePin{Version: fixture.version, BundleSHA256: digestBytes(index)}
}

func engineFixture(t *testing.T, engine, version, image string) []byte {
	t.Helper()
	if engine == "compose" {
		return fmt.Appendf(nil, "services:\n  fixture:\n    image: %s\n    pull_policy: never\n    labels:\n      example.invalid/version: %q\n", image, version)
	}
	chart := ociTar(t, map[string][]byte{
		"fixture/Chart.yaml":         []byte("apiVersion: v2\nname: fixture\nversion: " + version + "\n"),
		"fixture/templates/pod.yaml": fmt.Appendf(nil, "apiVersion: v1\nkind: Pod\nmetadata:\n  name: fixture\nspec:\n  containers:\n    - name: fixture\n      image: example.invalid/fixture@%s\n      imagePullPolicy: Never\n", image),
	}, "")
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(chart); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestVerifyInitialReleaseChecksSelectedIdentityAndCompatibility(t *testing.T) {
	for _, engine := range []string{"compose", "helm"} {
		t.Run(engine, func(t *testing.T) {
			root, key, private := transitionCatalog(t)
			pin := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: engine})
			request := InitialReleaseRequest{AppID: "example-app", PlatformVersion: "1.2.0", Pin: pin}
			var first InstallBundle
			for attempt := range 2 {
				release, err := VerifyInitialRelease(t.Context(), root, key, testTransitionBudget, request)
				if err != nil || release.Pin != pin || release.Manifest.Artifact.Engine != engine || len(release.Archive.Images) != 1 {
					t.Fatalf("selected release did not pass full verification: %v", err)
				}
				if attempt == 0 {
					first = release
				} else if first.Manifest.Artifact != release.Manifest.Artifact ||
					first.Archive.Digest != release.Archive.Digest || !slices.Equal(first.Archive.Images, release.Archive.Images) {
					t.Fatal("retry returned different signed content")
				}
			}
			for _, candidate := range []InitialReleaseRequest{
				{AppID: "different-app", PlatformVersion: request.PlatformVersion, Pin: pin},
				{AppID: request.AppID, PlatformVersion: "0.9.0", Pin: pin},
			} {
				if release, err := VerifyInitialRelease(t.Context(), root, key, testTransitionBudget, candidate); !errors.Is(err, ErrReleaseTransition) || release.Pin != (ReleasePin{}) {
					t.Fatalf("selected an unrelated or incompatible release: %v", err)
				}
			}

			replacement := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: pin.Version, engine: engine, variant: "-republished"})
			if release, err := VerifyInitialRelease(t.Context(), root, key, testTransitionBudget, request); !errors.Is(err, ErrReleaseTransition) || release.Pin != (ReleasePin{}) {
				t.Fatalf("accepted different signed bytes under the same version: %v", err)
			}

			if err := root.WriteFile(replacement.Version+"/"+first.Manifest.Artifact.Filename(), []byte("changed artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			request.Pin = replacement
			if release, err := VerifyInitialRelease(t.Context(), root, key, testTransitionBudget, request); !errors.Is(err, ErrBundle) || release.Pin != (ReleasePin{}) {
				t.Fatalf("accepted a changed signed payload: %v", err)
			}
		})
	}
}

func TestVerifyReleaseTransitionSelectsAndRecoversExactBundles(t *testing.T) {
	for _, engine := range []string{"compose", "helm"} {
		t.Run(engine, func(t *testing.T) {
			root, key, private := transitionCatalog(t)
			first := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: engine})
			second := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.1.0", engine: engine, predecessors: []ReleasePin{first}})
			third := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "2.0.0", engine: engine, predecessors: []ReleasePin{second}})
			originalRoot, err := root.OpenRoot(first.Version)
			if err != nil {
				t.Fatal(err)
			}
			defer originalRoot.Close()
			original, err := VerifyInstallBundle(t.Context(), originalRoot, key, testTransitionBudget)
			if err != nil || original.Pin != first {
				t.Fatalf("initial installation did not return its exact pin: %v", err)
			}
			request := TransitionRequest{AppID: "example-app", PlatformVersion: "1.2.0", Installed: first}
			for range 2 {
				upgrade, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget, request)
				if err != nil || upgrade.Pin != second || upgrade.Manifest.Artifact.SHA256 == original.Manifest.Artifact.SHA256 ||
					upgrade.Archive.Digest == original.Archive.Digest {
					t.Fatalf("next adjacent artifact was not verified: %v", err)
				}
				request.UpgradeTo = &upgrade.Pin
			}
			next, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget,
				TransitionRequest{AppID: request.AppID, PlatformVersion: request.PlatformVersion, Installed: second})
			if err != nil || next.Pin != third {
				t.Fatalf("second edge was not selected: %v", err)
			}
			if err := root.Remove(second.Version + "/" + (Artifact{Engine: engine}).Filename()); err != nil {
				t.Fatal(err)
			}
			request.Installed, request.UpgradeTo, request.RollbackTo = second, nil, &first
			for range 2 {
				rollback, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget, request)
				if err != nil || rollback.Pin != original.Pin || rollback.Manifest.Artifact != original.Manifest.Artifact ||
					rollback.Archive.Digest != original.Archive.Digest || !slices.Equal(rollback.Archive.Images, original.Archive.Images) {
					t.Fatalf("rollback did not recover the original signed bytes: %v", err)
				}
			}
		})
	}
}

func TestVerifyReleaseTransitionRefusesUnsafeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, message string
	}{
		{"missing installed", "local directory"},
		{"missing expected", "expected signed upgrade"},
		{"missing rollback", "local directory"},
		{"missing recovery payload", "installed recovery bundle"},
		{"fork", "multiple direct successors"},
		{"skip", "expected signed upgrade"},
		{"incompatible", "incompatible"},
		{"different engine", "same workload engine"},
		{"wrong directory", "signed app and version"},
		{"unsigned", "verify catalog version"},
		{"reissued rollback", "differs from its retained"},
		{"tampered image", "signed archive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, key, private := transitionCatalog(t)
			first := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: "compose"})
			fixture := catalogReleaseFixture{version: "1.1.0", engine: "compose", predecessors: []ReleasePin{first}}
			switch tc.name {
			case "incompatible":
				fixture.before = "1.2.0"
			case "different engine":
				fixture.engine = "helm"
			}
			second := writeTransitionRelease(t, root, private, fixture)
			request := TransitionRequest{AppID: "example-app", PlatformVersion: "1.2.0", Installed: first}
			var editErr error
			switch tc.name {
			case "missing installed":
				request.Installed.Version = "0.1.0"
			case "missing expected":
				request.UpgradeTo = &ReleasePin{Version: "1.3.0", BundleSHA256: second.BundleSHA256}
			case "missing rollback":
				request.Installed, request.RollbackTo = second, &first
				editErr = root.Rename(first.Version, "not-retained")
			case "missing recovery payload":
				editErr = root.Remove(first.Version + "/compose.yaml")
			case "fork":
				request.UpgradeTo = &second
				writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.2.0", engine: "compose", predecessors: []ReleasePin{first}})
			case "skip":
				third := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "2.0.0", engine: "compose", predecessors: []ReleasePin{second}})
				request.UpgradeTo = &third
			case "wrong directory":
				editErr = root.Rename(second.Version, "1.3.0")
			case "unsigned":
				editErr = root.Remove(second.Version + "/bundle.sig")
			case "reissued rollback":
				request.Installed, request.RollbackTo = second, &first
				writeTransitionRelease(t, root, private, catalogReleaseFixture{version: first.Version, engine: "compose", variant: "reissued"})
			case "tampered image":
				path := second.Version + "/" + ImageArchiveName
				archive, err := root.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				editErr = root.WriteFile(path, append(archive, 0), 0600)
			}
			if editErr != nil {
				t.Fatal(editErr)
			}
			result, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget, request)
			if err == nil || !strings.Contains(err.Error(), tc.message) || result.Pin != (ReleasePin{}) {
				t.Fatalf("wanted refusal containing %q, got %v", tc.message, err)
			}
		})
	}
}

func TestImportRefusesAnotherSignedReleaseAfterSelection(t *testing.T) {
	root, key, private := transitionCatalog(t)
	first := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: "compose"})
	second := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.1.0", engine: "compose", predecessors: []ReleasePin{first}})
	verified, err := VerifyReleaseTransition(t.Context(), root, key, testTransitionBudget,
		TransitionRequest{AppID: "example-app", PlatformVersion: "1.2.0", Installed: first, UpgradeTo: &second})
	if err != nil {
		t.Fatal(err)
	}
	writeTransitionRelease(t, root, private, catalogReleaseFixture{version: second.Version, engine: "compose", predecessors: []ReleasePin{first}, variant: "reissued"})
	result, err := ImportInstallImage(t.Context(), root.Name()+"/"+second.Version, key, ImageImportOptions{
		MaximumPayloadBytes: testTransitionBudget, MaximumExpandedBytes: testTransitionBudget,
		Timeout: time.Second, ExpectedPin: &verified.Pin,
	})
	if !errors.Is(err, ErrImageImport) || !strings.Contains(err.Error(), "differs from the selected release pin") || result.Pin != (ReleasePin{}) {
		t.Fatalf("import accepted another signed release or reached the engine: %v", err)
	}
}
