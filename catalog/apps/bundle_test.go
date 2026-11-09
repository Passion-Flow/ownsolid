package apps

import (
	"context"
	"crypto/ed25519"
	"encoding/json/v2"
	"errors"
	"os"
	"strings"
	"testing"
)

type signedFixture struct {
	root    *os.Root
	key     ed25519.PublicKey
	private ed25519.PrivateKey
	limit   int64
}

func signedBundle(t *testing.T, engine string) signedFixture {
	t.Helper()
	// These fixture bytes exercise signature and hash verification. The deployer
	// will separately validate the native payload formats and image identities.
	artifact := []byte("synthetic " + engine + " payload")
	images := []byte("synthetic image archive")
	manifest := strings.Replace(fixtureManifest, `"compose"`, `"`+engine+`"`, 1)
	manifest = strings.Replace(manifest, "sha256:"+strings.Repeat("a", 64), digestBytes(artifact), 1)
	root := releaseDirectory(t, manifest, fixtureChinese)
	for name, data := range map[string][]byte{
		(Artifact{Engine: engine}).Filename(): artifact,
		ImageArchiveName:                      images,
	} {
		if err := root.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	key, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(bundleIndex{
		SchemaVersion: 1, Manifest: digestBytes([]byte(manifest)),
		English: digestBytes([]byte(fixtureEnglish)), Chinese: digestBytes([]byte(fixtureChinese)),
		Images: digestBytes(images),
	})
	if err != nil {
		t.Fatal(err)
	}
	signFixtureIndex(t, root, private, index)
	return signedFixture{root, key, private, int64(len(artifact) + len(images))}
}

func signFixtureIndex(t *testing.T, root *os.Root, key ed25519.PrivateKey, index []byte) {
	t.Helper()
	signature := ed25519.Sign(key, append([]byte(BundleSigningDomain), index...))
	for name, raw := range map[string][]byte{"bundle.json": index, "bundle.sig": signature} {
		if err := root.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFixtureLicenseNote(t *testing.T, root *os.Root, path string, note *string) string {
	t.Helper()
	raw, err := root.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var messages Messages
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	messages.LicenseNote = note
	updated := ociJSON(t, messages)
	if err := root.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	return digestBytes(updated)
}

func TestVerifySignedLicenseNotes(t *testing.T) {
	english := "MIT. Retain the publisher attribution."
	chinese := "MIT. \u8bf7\u4fdd\u7559\u53d1\u5e03\u8005\u7f72\u540d\u3002"
	empty, control, oversized := "", "notice\x1b", strings.Repeat("\u8bb8", licenseNoteRunes+1)
	for _, tc := range []struct {
		name      string
		en, zh    *string
		wantError bool
	}{
		{name: "paired", en: &english, zh: &chinese},
		{name: "legacy"},
		{name: "missing Chinese", en: &english, wantError: true},
		{name: "missing English", zh: &chinese, wantError: true},
		{name: "empty", en: &empty, zh: &chinese, wantError: true},
		{name: "control characters", en: &control, zh: &chinese, wantError: true},
		{name: "over limit", en: &english, zh: &oversized, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := newImageFixture(t, "amd64", false)
			fixture := signOCI(t, ociTar(t, image.files, ""), image.image)
			index, err := readIndex(fixture.root, fixture.key)
			if err != nil {
				t.Fatal(err)
			}
			if tc.en != nil || tc.zh != nil {
				index.English = writeFixtureLicenseNote(t, fixture.root, englishName, tc.en)
				index.Chinese = writeFixtureLicenseNote(t, fixture.root, chineseName, tc.zh)
				signFixtureIndex(t, fixture.root, fixture.private, ociJSON(t, index))
			}
			result, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
			if tc.wantError {
				if !errors.Is(err, ErrBundle) || !errors.Is(err, ErrLocale) || result.Pin != (ReleasePin{}) {
					t.Fatalf("invalid note returned a verified release: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.en == nil {
				if result.English.LicenseNote != nil || result.SimplifiedChinese.LicenseNote != nil {
					t.Fatal("the legacy release unexpectedly contains license notes")
				}
				return
			}
			if result.English.LicenseNote == nil || result.SimplifiedChinese.LicenseNote == nil ||
				*result.English.LicenseNote != *tc.en || *result.SimplifiedChinese.LicenseNote != *tc.zh {
				t.Fatal("the verified release did not return both exact notes")
			}
			changed := "A different notice"
			writeFixtureLicenseNote(t, fixture.root, chineseName, &changed)
			if _, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit); !errors.Is(err, ErrBundle) {
				t.Fatalf("an unsigned note change was accepted: %v", err)
			}
		})
	}
}

func TestVerifySignedBundle(t *testing.T) {
	for _, engine := range []string{"helm", "compose"} {
		t.Run(engine, func(t *testing.T) {
			fixture := signedBundle(t, engine)
			release, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
			if err != nil || release.Manifest.Artifact.Engine != engine || release.Manifest.ID != "example-app" {
				t.Fatalf("cannot verify local release: %v", err)
			}
			if _, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit-1); !errors.Is(err, ErrBundle) {
				t.Fatalf("accepted payloads above the total byte limit: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := VerifyBundle(ctx, fixture.root, fixture.key, fixture.limit); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled verification returned %v", err)
			}
		})
	}
}

func TestRejectTamperedSignedBundle(t *testing.T) {
	for _, filename := range []string{"bundle.json", "bundle.sig", manifestName, englishName, chineseName, "chart.tgz", ImageArchiveName} {
		t.Run(filename, func(t *testing.T) {
			fixture := signedBundle(t, "helm")
			original, err := fixture.root.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			original[len(original)-1] ^= 1
			if err := fixture.root.WriteFile(filename, original, 0600); err != nil {
				t.Fatal(err)
			}
			release, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
			if !errors.Is(err, ErrBundle) || release.Manifest.ID != "" {
				t.Fatalf("tampered file was admitted: %v", err)
			}
		})
	}
}

func TestRejectUntrustedOrIncompleteBundle(t *testing.T) {
	fixture := signedBundle(t, "compose")
	other, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(t.Context(), fixture.root, other, fixture.limit); !errors.Is(err, ErrBundle) {
		t.Fatalf("wrong publisher key was admitted: %v", err)
	}

	index, err := fixture.root.ReadFile("bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	signFixtureIndex(t, fixture.root, fixture.private, append([]byte(`{"schema_version":1,`), index[1:]...))
	if _, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit); !errors.Is(err, ErrBundle) {
		t.Fatalf("duplicate index key was admitted: %v", err)
	}
	signFixtureIndex(t, fixture.root, fixture.private, index)

	if err := fixture.root.Rename(ImageArchiveName, "other.tar"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit); !errors.Is(err, ErrBundle) {
		t.Fatalf("missing image archive was admitted: %v", err)
	}
	if err := fixture.root.Symlink("other.tar", ImageArchiveName); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBundle(t.Context(), fixture.root, fixture.key, fixture.limit); !errors.Is(err, ErrBundle) {
		t.Fatalf("symlink substitution was admitted: %v", err)
	}
}
