package scope

import (
	"context"
	"os"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestBackupPublicationRecoversOnlyExactPrivateBytesAndKnownLinkPair(t *testing.T) {
	for _, state := range []string{"partial", "pair", "final", "wrong-partial", "different-files", "outside-link", "bad-metadata"} {
		t.Run(state, func(t *testing.T) {
			parent := t.TempDir()
			os.Chmod(parent, 0700)
			root, e := os.OpenRoot(parent)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			payload, metadata := []byte("Explicit synthetic complete transport bytes"), []byte("Explicit synthetic stream observation\n")
			if state != "final" {
				data := payload
				if state == "wrong-partial" {
					data = []byte("incomplete")
				}
				if e := writeBackupFile(root, "models.tar.partial", data); e != nil {
					t.Fatal(e)
				}
			}
			if state == "pair" {
				if e := root.Link("models.tar.partial", "models.tar"); e != nil {
					t.Fatal(e)
				}
			}
			if state == "final" || state == "different-files" {
				if e := writeBackupFile(root, "models.tar", payload); e != nil {
					t.Fatal(e)
				}
			}
			if state == "outside-link" {
				if e := root.Link("models.tar.partial", "unknown"); e != nil {
					t.Fatal(e)
				}
			}
			if state == "bad-metadata" {
				if e := writeBackupFile(root, "models.json", []byte("preserve conflicting metadata")); e != nil {
					t.Fatal(e)
				}
			}
			e = finishBackupPublication(context.Background(), root, "models.tar", "models.json", int64(len(payload)), wire.Digest(payload), metadata)
			if state == "partial" || state == "pair" || state == "final" {
				if e != nil {
					t.Fatal(e)
				}
				if f, e := backupHashFile(context.Background(), root, "models.tar", int64(len(payload))); e != nil || f.SHA256 != wire.Digest(payload) {
					t.Fatal(f, e)
				}
				if _, e := root.Lstat("models.tar.partial"); !os.IsNotExist(e) {
					t.Fatal("partial alias retained")
				}
				if e = finishBackupPublication(context.Background(), root, "models.tar", "models.json", int64(len(payload)), wire.Digest(payload), metadata); e != nil {
					t.Fatal("idempotent observed publication", e)
				}
			} else if e == nil {
				t.Fatal("unsafe partial publication promoted")
			}
		})
	}
}
