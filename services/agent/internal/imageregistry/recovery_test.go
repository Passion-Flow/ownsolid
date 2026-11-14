package imageregistry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Structural test metadata only. The proof and archive hashes are not genuine
// release bytes and must never pass the real signed-proof loader.
func registrySyntheticManifest() wire.RegistryManifest {
	r := wire.ApprovedInferenceRecipe()
	return wire.RegistryManifest{ReleaseProof: json.RawMessage(`{"explicit_test":true}`), PinID: "12345678-1234-1234-1234-123456789abc", GrantID: "22345678-1234-1234-1234-123456789abc", DeploymentID: "32345678-1234-1234-1234-123456789abc", Revision: 1,
		ImageManifestDigest: r.ImageManifestDigest, ImageConfigDigest: r.ImageConfigDigest, ArchiveSHA256: strings.Repeat("1", 64), ArchiveBytes: 1, ExpandedBytes: 1, SourceArchiveSHA256: strings.Repeat("2", 64), SourceManifestSHA256: strings.Repeat("3", 64), OS: r.OS, Architecture: r.Architecture, ExpiresAt: time.Now().UTC().Add(time.Hour)}
}

func TestRegistryTaskIntentAllowsOnlyGrantRotation(t *testing.T) {
	original := registrySyntheticManifest()
	for _, name := range []string{"unchanged", "grant", "expiry", "pin", "deployment", "revision", "proof", "manifest", "config", "archive_sha", "archive_bytes", "expanded_bytes", "source_archive", "source_manifest", "os", "architecture"} {
		t.Run(name, func(t *testing.T) {
			current := original
			switch name {
			case "grant":
				current.GrantID = "42345678-1234-1234-1234-123456789abc"
			case "expiry":
				current.ExpiresAt = current.ExpiresAt.Add(time.Hour)
			case "pin":
				current.PinID = current.GrantID
			case "deployment":
				current.DeploymentID = current.PinID
			case "revision":
				current.Revision++
			case "proof":
				current.ReleaseProof = json.RawMessage(`{"explicit_test":false}`)
			case "manifest":
				current.ImageManifestDigest = "changed"
			case "config":
				current.ImageConfigDigest = "changed"
			case "archive_sha":
				current.ArchiveSHA256 = "changed"
			case "archive_bytes":
				current.ArchiveBytes++
			case "expanded_bytes":
				current.ExpandedBytes++
			case "source_archive":
				current.SourceArchiveSHA256 = "changed"
			case "source_manifest":
				current.SourceManifestSHA256 = "changed"
			case "os":
				current.OS = "changed"
			case "architecture":
				current.Architecture = "changed"
			}
			want := name == "unchanged" || name == "grant" || name == "expiry"
			if sameTaskImageIntent(original, current) != want {
				t.Fatal("immutable intent changed", name)
			}
		})
	}
}

func TestRegistryPrivateMetadataBoundsAndContainment(t *testing.T) {
	directory, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(directory, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	for _, name := range []string{"valid_large", "mode", "symlink", "directory", "empty", "oversize", "duplicate", "malformed", "unknown", "escape"} {
		t.Run(name, func(t *testing.T) {
			var e error
			raw := []byte(`{"payload":"` + strings.Repeat("x", 68000) + `"}`)
			maximum := int64(len(raw))
			filename := name + ".json"
			switch name {
			case "empty":
				raw = nil
			case "oversize":
				maximum--
			case "duplicate":
				raw = []byte(`{"payload":"a","payload":"b"}`)
			case "malformed":
				raw = []byte(`{`)
			case "unknown":
				raw = []byte(`{"unknown":false}`)
			case "escape":
				filename = "../escape.json"
			}
			if name == "directory" {
				e = root.Mkdir(filename, 0700)
			} else if name == "symlink" {
				e = root.Symlink("valid_large.json", filename)
			} else if name != "escape" {
				e = os.WriteFile(filepath.Join(directory, filename), raw, 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			if name == "mode" {
				if e = root.Chmod(filename, 0644); e != nil {
					t.Fatal(e)
				}
			}
			var value struct {
				Payload string `json:"payload"`
			}
			e = readPrivateJSON(root, filename, maximum, &value)
			if (e == nil) != (name == "valid_large") {
				t.Fatal("private metadata boundary changed", name, e)
			}
			if name == "valid_large" && len(value.Payload) != 68000 {
				t.Fatal("metadata truncated")
			}
		})
	}
}

func TestRegistryStoredTaskRejectsInvalidOrUnprovenJournal(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	c := Defaults()
	c.Root = root
	l := &Loader{config: c} // Explicit negative fixture: no Docker or control client.
	for _, task := range []string{"", "../task", "12345678-1234-1234-1234-123456789abc"} {
		if e = l.ReconcileStoredTask(context.Background(), task); e == nil {
			t.Fatal("invalid/unproven journal accepted")
		}
	}
	job := filepath.Join(root, "task-12345678-1234-1234-1234-123456789abc")
	if e = os.Mkdir(job, 0700); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(registrySyntheticManifest())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(job, "intent.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = l.ReconcileStoredTask(context.Background(), "12345678-1234-1234-1234-123456789abc"); e == nil {
		t.Fatal("synthetic proof gained image approval")
	}
}

func TestRegistryOfflineManifestIsProtectedStructuralInput(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c := Defaults()
	c.Root = filepath.Join(root, "imports")
	c.DockerDataRoot = filepath.Join(root, "docker")
	c.Verification.MountRoot = root
	c.Verification.TrustFile = filepath.Join(root, "trust.json")
	m := wire.RegistryOfflineManifest{ReferenceID: "42345678-1234-1234-1234-123456789abc", Image: registrySyntheticManifest().ImageIdentity()}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(root, "manifest.json")
	if e = os.WriteFile(p, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadOfflineManifest(c, p); e != nil {
		t.Fatal("structural manifest refused", e)
	}
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadOfflineManifest(c, p); e == nil {
		t.Fatal("public manifest adopted")
	}
	if _, e = ReadOfflineManifest(c, "manifest.json"); e == nil {
		t.Fatal("relative manifest adopted")
	}
}
