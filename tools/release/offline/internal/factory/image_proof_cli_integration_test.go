//go:build factory_image_proof_cli_integration

package factory

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualFactoryCLIExportsOnlyFullyVerifiedPreparedImageProof(t *testing.T) {
	binary, sha := os.Getenv("PROJECT_ONE_TEST_OFFLINE_FACTORY"), os.Getenv("PROJECT_ONE_TEST_OFFLINE_FACTORY_SHA256")
	raw, e := os.ReadFile(binary)
	if e != nil || !filepath.IsAbs(binary) || !wire.ValidSHA256(sha) || wire.Digest(raw) != sha {
		t.Fatal("current source-bound factory binary required", e)
	}
	for _, fault := range []string{"valid", "unrelated-asset-missing", "unrelated-asset-tampered", "signature-tampered", "journal-missing", "wrong-archive", "wrong-manifest", "unknown-image", "non-image", "missing-prepared", "extra-argument"} {
		t.Run(fault, func(t *testing.T) {
			c, manifest, _ := imageProofFixture(t)
			if selected := os.Getenv("PROJECT_ONE_TEST_FACTORY_MOUNT_ROOT"); selected != "" && c.MountRoot != selected {
				t.Fatal("test fixtures escaped the explicitly selected actual mount")
			}
			// The outer runner admits the real mount and fixture peak separately;
			// exercise its explicit fixed reserve without an unrelated size floor.
			c.MinimumFreeBytes = 32 << 30
			put(t, c.ManifestFile, Encode(manifest))
			built, e := Build(t.Context(), c)
			if e != nil {
				t.Fatal(e)
			}
			prepared := filepath.Join(filepath.Dir(c.OutputFile), "prepared")
			if _, e = Unpack(t.Context(), c, c.OutputFile, prepared, built.ArchiveSHA256, built.ManifestSHA256); e != nil {
				t.Fatal(e)
			}
			assetID := manifest.Assets[len(manifest.Assets)-1].ID
			archiveSHA, manifestSHA := built.ArchiveSHA256, built.ManifestSHA256
			switch fault {
			case "unrelated-asset-missing":
				e = os.Remove(filepath.Join(prepared, manifest.Assets[0].Path))
			case "unrelated-asset-tampered":
				put(t, filepath.Join(prepared, manifest.Assets[0].Path), []byte("unrelated asset corruption"))
			case "signature-tampered":
				put(t, filepath.Join(prepared, "signature.json"), []byte("tampered signature"))
			case "journal-missing":
				e = os.Remove(prepared + ".journal.jsonl")
			case "wrong-archive":
				archiveSHA = strings.Repeat("0", 64)
			case "wrong-manifest":
				manifestSHA = strings.Repeat("0", 64)
			case "unknown-image":
				assetID = "not-signed"
			case "non-image":
				assetID = manifest.Assets[0].ID
			case "missing-prepared":
				prepared += "-absent"
			}
			if e != nil {
				t.Fatal(e)
			}
			// The exporter has no issuer key and does not need the input manifest.
			c.PrivateKeyFile, c.ManifestFile = "", ""
			config := filepath.Join(filepath.Dir(c.OutputFile), "proof-config.json")
			put(t, config, Encode(c))
			args := []string{"image-proof", "--config", config, "--prepared", prepared, "--archive-sha256", archiveSHA, "--manifest-sha256", manifestSHA, "--asset-id", assetID}
			if fault == "extra-argument" {
				args = append(args, "unexpected")
			}
			before := proofCLIState(t, filepath.Dir(c.OutputFile))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			e = cmd.Run()
			if after := proofCLIState(t, filepath.Dir(c.OutputFile)); !reflect.DeepEqual(before, after) {
				t.Fatal("read-only proof export changed release/prepared authority")
			}
			if fault != "valid" {
				if e == nil || stdout.Len() != 0 || !strings.HasPrefix(strings.TrimSpace(stderr.String()), "RELEASE_") {
					t.Fatal("invalid origin exported proof", e, stdout.String(), stderr.String())
				}
				return
			}
			var proof ImageProof
			if e != nil || stderr.Len() != 0 || wire.StrictJSON(stdout.Bytes(), &proof) != nil {
				t.Fatal("native proof export", e, stderr.String())
			}
			got, e := VerifyImageProof(t.Context(), c, proof)
			if e != nil || got.ManifestSHA256 != built.ManifestSHA256 || got.Asset.ID != assetID || !bytes.Equal(proof.Manifest, Encode(manifest)) {
				t.Fatal("export lost original signed bytes", got, e)
			}
		})
	}
}

func proofCLIState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	if e := filepath.WalkDir(root, func(name string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Type().IsRegular() {
			raw, e := os.ReadFile(name)
			if e != nil {
				return e
			}
			state[name] = wire.Digest(raw)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return state
}
