package training_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
)

func TestTrainingInitializedStoreRejectsMutatedNamespaceAndOwnership(t *testing.T) {
	for _, fault := range []string{"tenant", "uid", "gid", "uid_overflow", "gid_overflow", "nonempty", "unknown", "linked", "mode", "lock_bytes"} {
		t.Run(fault, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "store")
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
			tenant, uid, gid := uuid.NewString(), os.Geteuid(), os.Getegid()
			if e := training.InitializeStore(root, tenant, uid, gid); e != nil {
				t.Fatal(e)
			}
			before, e := os.ReadFile(filepath.Join(root, "namespace.json"))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = training.VerifyInitializedStore(root, tenant, uid, gid); e != nil {
				t.Fatal(e)
			}
			var mutation error
			switch fault {
			case "tenant":
				tenant = uuid.NewString()
			case "uid":
				uid++
			case "gid":
				gid++
			case "uid_overflow":
				uid = 2147483648
			case "gid_overflow":
				gid = 2147483648
			case "nonempty":
				mutation = os.WriteFile(filepath.Join(root, "uploads", "preserved"), []byte("synthetic"), 0600)
			case "unknown":
				mutation = os.WriteFile(filepath.Join(root, "unknown"), nil, 0600)
			case "linked":
				mutation = os.Link(filepath.Join(root, "namespace.json"), filepath.Join(filepath.Dir(root), "preserved"))
			case "mode":
				mutation = os.Chmod(filepath.Join(root, "checkpoints"), 0750)
			case "lock_bytes":
				mutation = os.WriteFile(filepath.Join(root, "owner.lock"), []byte("not empty"), 0600)
			}
			if mutation != nil {
				t.Fatal(mutation)
			}
			if _, e = training.VerifyInitializedStore(root, tenant, uid, gid); e == nil {
				t.Fatal("mutated initialization accepted")
			}
			after, e := os.ReadFile(filepath.Join(root, "namespace.json"))
			if e != nil || string(after) != string(before) {
				t.Fatal("verification changed namespace", e)
			}
		})
	}
}
