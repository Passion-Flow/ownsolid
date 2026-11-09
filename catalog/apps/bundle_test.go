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
