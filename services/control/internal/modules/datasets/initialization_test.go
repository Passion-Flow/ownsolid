package datasets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFreshDatasetNamespaceVerificationRejectsChangedInventoryAndMetadata(t *testing.T) {
	for _, fault := range []string{"", "parent-mode", "marker-mode", "marker-link", "foreign-tenant", "nonempty", "lock-content", "foreign-gid"} {
		t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			tenant := uuid.NewString()
			uid, gid := os.Geteuid(), os.Getegid()
			if e := InitializeStore(root, tenant, uid, gid); e != nil {
				t.Fatal(e)
			}
			switch fault {
			case "parent-mode":
				os.Chmod(root, 0755)
			case "marker-mode":
				os.Chmod(filepath.Join(root, markerFile), 0644)
			case "marker-link":
				os.Link(filepath.Join(root, markerFile), filepath.Join(t.TempDir(), "retained-link"))
			case "foreign-tenant":
				tenant = uuid.NewString()
			case "nonempty":
				os.WriteFile(filepath.Join(root, "objects", "foreign"), []byte("synthetic"), 0600)
			case "lock-content":
				os.WriteFile(filepath.Join(root, lockFile), []byte("changed"), 0600)
			case "foreign-gid":
				gid++
			}
			id, e := VerifyInitializedStore(root, tenant, uid, gid)
			if fault == "" {
				if e != nil || id == "" {
					t.Fatal(id, e)
				}
			} else if e == nil {
				t.Fatal("unsafe namespace accepted", fault)
			}
		})
	}
}

func TestFreshDatasetNamespaceRejectsInvalidVersionIdentityAndPartialDocument(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":2,"store_id":"%s","tenant_id":"%s"}`,
		`{"schema_version":1,"store_id":"invalid","tenant_id":"%s"}`,
		`{"schema_version":1,"store_id":"%s","tenant_id":"%s","kind":"foreign"}`,
		`{"schema_version":1,`,
	} {
		t.Run(raw, func(t *testing.T) {
			root := t.TempDir()
			tenant := uuid.NewString()
			uid, gid := os.Geteuid(), os.Getegid()
			if e := InitializeStore(root, tenant, uid, gid); e != nil {
				t.Fatal(e)
			}
			value := strings.Replace(raw, "%s", uuid.NewString(), 1)
			if strings.Contains(raw, `"store_id":"invalid"`) {
				value = strings.Replace(raw, "%s", tenant, 1)
			} else {
				value = strings.Replace(value, "%s", tenant, 1)
			}
			if e := os.WriteFile(filepath.Join(root, markerFile), []byte(value), 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := VerifyInitializedStore(root, tenant, uid, gid); e == nil {
				t.Fatal("invalid namespace accepted")
			}
		})
	}
}
