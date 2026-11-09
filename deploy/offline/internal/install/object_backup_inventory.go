package install

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"

	jsonv2 "encoding/json/v2"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	objectInventoryName     = "object-inventory.json"
	objectBackupCopyDir     = "objects"
	objectInventoryMaxRows  = 100_000
	objectInventoryMaxBytes = 64 << 20
	objectBackupMaxFile     = 5 << 40
)

var (
	objectBackupUUID         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	errObjectBackupScope     = errors.New("INSTALL_OBJECT_BACKUP_SCOPE_MISMATCH: use the trusted source operation and its exact copy confirmation")
	errObjectBackupInventory = errors.New("INSTALL_OBJECT_BACKUP_INVENTORY_INVALID: stage the exact Control-verified object inventory")
	errObjectBackupCopy      = errors.New("INSTALL_OBJECT_BACKUP_COPY_INVALID: verify the complete immutable copy for this operation")
)

// These private types mirror the version 1 artifactstores.BackupInventory
// digest input. Field order and Go JSON v1 escaping are part of that digest.
type objectBackupInventory struct {
	FormatVersion   int                  `json:"format_version"`
	Database        string               `json:"database"`
	DatabaseOID     int64                `json:"database_oid"`
	Schema          string               `json:"schema"`
	TenantID        string               `json:"tenant_id"`
	StoreID         string               `json:"store_id"`
	BackendSHA256   string               `json:"backend_sha256"`
	Objects         []objectBackupObject `json:"objects"`
	InventorySHA256 string               `json:"inventory_sha256"`
}

type objectBackupObject struct {
	WorkspaceID string  `json:"workspace_id"`
	ObjectID    string  `json:"object_id"`
	State       string  `json:"state"`
	Revision    int64   `json:"revision"`
	SizeBytes   int64   `json:"size_bytes"`
	SHA256      string  `json:"sha256"`
	ETag        *string `json:"etag"`
}

func validObjectBackupID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && objectBackupUUID.MatchString(value)
}

func readObjectBackupInventory(ctx context.Context, root *os.Root, pin objectBackupPin) (map[string]int64, error) {
	raw, err := readObjectBackupMetadata(ctx, root, objectInventoryName, objectInventoryMaxBytes)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errObjectBackupInventory
	}
	var inventory objectBackupInventory
	if jsonv2.Unmarshal(raw, &inventory, jsonv2.RejectUnknownMembers(true)) != nil ||
		inventory.FormatVersion != 1 || inventory.Objects == nil || len(inventory.Objects) > objectInventoryMaxRows ||
		inventory.InventorySHA256 != pin.InventorySHA256 {
		return nil, errObjectBackupInventory
	}
	inventory.InventorySHA256 = ""
	canonical, err := json.Marshal(inventory)
	if err != nil || wire.Digest(canonical) != pin.InventorySHA256 {
		return nil, errObjectBackupInventory
	}
	scope := pin.Scope
	if inventory.Database != databaseName || inventory.Schema != "public" || inventory.DatabaseOID != int64(scope.DatabaseOID) ||
		inventory.TenantID != scope.TenantID || inventory.StoreID != scope.StoreID || inventory.BackendSHA256 != scope.BackendSHA256 {
		return nil, errObjectBackupScope
	}
	copy := make(map[string]int64)
	var previous string
	for _, object := range inventory.Objects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key := object.WorkspaceID + "/" + object.ObjectID
		if !validObjectBackupID(object.WorkspaceID) || !validObjectBackupID(object.ObjectID) || key <= previous ||
			object.Revision < 1 || object.SizeBytes < 1 || object.SizeBytes > objectBackupMaxFile || !wire.ValidSHA256(object.SHA256) {
			return nil, errObjectBackupInventory
		}
		previous = key
		switch object.State {
		case "ready":
			if object.ETag == nil || *object.ETag == "" || len(*object.ETag) > 1024 ||
				strings.ContainsAny(*object.ETag, "\r\n\x00") {
				return nil, errObjectBackupInventory
			}
			if size, exists := copy[object.SHA256]; exists && size != object.SizeBytes {
				return nil, errObjectBackupInventory
			}
			copy[object.SHA256] = object.SizeBytes
		case "aborted", "deleted":
			// Terminal rows are covered by the digest but need no byte copy.
		default:
			return nil, errObjectBackupInventory
		}
	}
	return copy, nil
}

func verifyObjectBackupCopy(ctx context.Context, root *os.Root, expected map[string]int64) error {
	before, err := root.Lstat(objectBackupCopyDir)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0o700 || !objectBackupOwner(before) {
		return errObjectBackupCopy
	}
	copyRoot, err := root.OpenRoot(objectBackupCopyDir)
	if err != nil {
		return errObjectBackupCopy
	}
	defer copyRoot.Close()
	dir, err := copyRoot.Open(".")
	if err != nil {
		return errObjectBackupCopy
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil || !sameObjectBackupFile(before, opened) {
		return errObjectBackupCopy
	}
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			size, wanted := expected[entry.Name()]
			if !wanted {
				return errObjectBackupCopy
			}
			if err := verifyObjectBackupFile(ctx, copyRoot, entry.Name(), size, entry.Name()); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errObjectBackupCopy
			}
			delete(expected, entry.Name())
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errObjectBackupCopy
		}
	}
	if len(expected) != 0 || !unchangedObjectBackupFile(root, objectBackupCopyDir, dir, before) {
		return errObjectBackupCopy
	}
	return nil
}
