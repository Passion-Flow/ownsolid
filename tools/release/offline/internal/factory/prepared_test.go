package factory

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPreparedInventoryFullReverification(t *testing.T) {
	c, r, destination := preparedFixture(t)
	if _, e := Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	result, e := VerifyPrepared(context.Background(), c, destination, r.ArchiveSHA256, r.ManifestSHA256)
	if e != nil || result.Status != "passed_verified_prepared_inventory" || result.Qualification != "synthetic_prepared_fixture_only_never_complete_platform_installation" || len(result.Manifest.Assets) != 2 {
		t.Fatal(result, e)
	}
}

func TestPreparedNestedDirectoryInventory(t *testing.T) {
	c, m, _ := setup(t)
	a := &m.Assets[1]
	old := filepath.Join(c.ArtifactRoot, a.Path)
	a.Path = "nested/other/payload"
	if e := os.MkdirAll(filepath.Dir(filepath.Join(c.ArtifactRoot, a.Path)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(old, filepath.Join(c.ArtifactRoot, a.Path)); e != nil {
		t.Fatal(e)
	}
	put(t, c.ManifestFile, Encode(m))
	r, e := Build(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	destination := filepath.Join(filepath.Dir(c.OutputFile), "nested-preparation")
	if _, e = Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyPrepared(context.Background(), c, destination, r.ArchiveSHA256, r.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
}
func TestPreparedInventoryCorruptionIsNeverAdopted(t *testing.T) {
	for _, fault := range []string{"payload", "missing", "extra-file", "extra-directory", "symlink", "hardlink", "manifest", "signature", "marker-status", "marker-hash", "journal-truncated", "journal-outcome", "journal-extra-event", "journal-duplicate-key", "journal-hash", "file-mode", "directory-mode", "deadline"} {
		t.Run(fault, func(t *testing.T) {
			c, r, destination := preparedFixture(t)
			if _, e := Unpack(context.Background(), c, c.OutputFile, destination, r.ArchiveSHA256, r.ManifestSHA256); e != nil {
				t.Fatal(e)
			}
			payload := filepath.Join(destination, r.Manifest.Assets[1].Path)
			journal := destination + ".journal.jsonl"
			marker := filepath.Join(destination, PreparedMarker)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			modify := func(name string, edit func([]byte) []byte) {
				raw, e := os.ReadFile(name)
				if e != nil {
					t.Fatal(e)
				}
				put(t, name, edit(raw))
			}
			switch fault {
			case "payload":
				modify(payload, func(raw []byte) []byte { raw[0] ^= 1; return raw })
			case "missing":
				os.Remove(payload)
			case "extra-file":
				put(t, filepath.Join(destination, "unknown"), []byte("preserved unknown bytes"))
			case "extra-directory":
				os.Mkdir(filepath.Join(destination, "unknown"), 0700)
			case "symlink":
				os.Remove(payload)
				os.Symlink(r.Manifest.Assets[0].Path, payload)
			case "hardlink":
				os.Link(payload, filepath.Join(filepath.Dir(destination), "additional-known-test-link"))
			case "manifest":
				modify(filepath.Join(destination, "manifest.json"), func(raw []byte) []byte { return append(raw, ' ') })
			case "signature":
				modify(filepath.Join(destination, "signature.json"), func(raw []byte) []byte { return bytes.Replace(raw, []byte("Ed25519"), []byte("unknown"), 1) })
			case "marker-status":
				modify(marker, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte("prepared_verified_inventory_only"), []byte("failed"), 1)
				})
			case "marker-hash":
				modify(marker, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte(r.ArchiveSHA256), bytes.Repeat([]byte("b"), 64), 1)
				})
			case "journal-truncated":
				modify(journal, func(raw []byte) []byte { return raw[:len(raw)-1] })
			case "journal-outcome":
				modify(journal, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte("prepared_verified_inventory_only"), []byte("failed"), 1)
				})
			case "journal-extra-event":
				modify(journal, func(raw []byte) []byte { return append(raw, raw...) })
			case "journal-duplicate-key":
				modify(journal, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte(`"status":"reserved"`), []byte(`"status":"reserved","status":"reserved"`), 1)
				})
			case "journal-hash":
				modify(journal, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte(r.ArchiveSHA256), bytes.Repeat([]byte("c"), 64), 1)
				})
			case "file-mode":
				os.Chmod(payload, 0644)
			case "directory-mode":
				os.Chmod(destination, 0755)
			case "deadline":
				cancel()
			}
			if _, e := VerifyPrepared(ctx, c, destination, r.ArchiveSHA256, r.ManifestSHA256); e == nil {
				t.Fatal("corrupt or untrusted preparation adopted")
			}
			if _, e := os.Lstat(destination); e != nil {
				t.Fatal("failed verification deleted destination")
			}
		})
	}
}
