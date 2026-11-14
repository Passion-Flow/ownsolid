package install

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	objectBackupCopyName    = "objects.tar"
	objectInventoryName     = "inventory.json"
	objectInventoryMaxRows  = 100_000
	objectInventoryMaxBytes = 64 << 20
	objectBackupMaxFile     = 5 << 40
	maxUSTARObjectBytes     = 1<<33 - 1
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

type objectBackupPayload struct {
	bytes  int64
	digest string
}

func validObjectBackupID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && objectBackupUUID.MatchString(value)
}

func checkObjectBackupInventory(ctx context.Context, raw []byte, pin objectBackupPin, observed map[string]objectBackupPayload) error {
	var inventory objectBackupInventory
	if jsonv2.Unmarshal(raw, &inventory, jsonv2.RejectUnknownMembers(true)) != nil ||
		inventory.FormatVersion != 1 || inventory.Objects == nil || len(inventory.Objects) > objectInventoryMaxRows ||
		inventory.InventorySHA256 != pin.InventorySHA256 {
		return errObjectBackupInventory
	}
	inventory.InventorySHA256 = ""
	canonical, err := json.Marshal(inventory)
	if err != nil || wire.Digest(canonical) != pin.InventorySHA256 {
		return errObjectBackupInventory
	}
	scope := pin.Scope
	if inventory.Database != databaseName || inventory.Schema != "public" || inventory.DatabaseOID != int64(scope.DatabaseOID) ||
		inventory.TenantID != scope.TenantID || inventory.StoreID != scope.StoreID || inventory.BackendSHA256 != scope.BackendSHA256 {
		return errObjectBackupScope
	}
	var previous string
	for _, object := range inventory.Objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := "objects/" + object.WorkspaceID + "/" + object.ObjectID
		if !validObjectBackupID(object.WorkspaceID) || !validObjectBackupID(object.ObjectID) || key <= previous ||
			object.Revision < 1 || object.SizeBytes < 1 || object.SizeBytes > objectBackupMaxFile || !wire.ValidSHA256(object.SHA256) {
			return errObjectBackupInventory
		}
		previous = key
		switch object.State {
		case "ready":
			if object.ETag == nil || *object.ETag == "" || len(*object.ETag) > 1024 ||
				strings.ContainsAny(*object.ETag, "\r\n\x00") {
				return errObjectBackupInventory
			}
			if observed[key] != (objectBackupPayload{object.SizeBytes, object.SHA256}) {
				return errObjectBackupCopy
			}
			delete(observed, key)
		case "aborted", "deleted":
			// Terminal rows are covered by the digest but have no byte entry.
		default:
			return errObjectBackupInventory
		}
	}
	if len(observed) != 0 {
		return errObjectBackupCopy
	}
	return nil
}

func validObjectBackupHeader(h *tar.Header) bool {
	if h.Typeflag != tar.TypeReg || h.Mode != 0o600 || h.Size < 1 || h.Linkname != "" ||
		h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || len(h.Xattrs) != 0 ||
		(h.Format != tar.FormatUSTAR && h.Format != tar.FormatPAX) {
		return false
	}
	if h.Size > maxUSTARObjectBytes {
		return len(h.PAXRecords) == 1 && h.PAXRecords["size"] == strconv.FormatInt(h.Size, 10)
	}
	return len(h.PAXRecords) == 0
}

func verifyObjectBackupCopy(ctx context.Context, root *os.Root, pin objectBackupPin) error {
	file, info, err := openObjectBackupFile(root, objectBackupCopyName)
	if err != nil {
		return errObjectBackupCopy
	}
	defer file.Close()
	if info.Size() != pin.CopySizeBytes {
		return errObjectBackupCopy
	}
	hash := sha256.New()
	input := &archiveCounter{Reader: io.TeeReader(io.LimitReader(archiveReader{ctx, file}, pin.CopySizeBytes+1), hash)}
	archive := tar.NewReader(input)
	observed := make(map[string]objectBackupPayload)
	var previous string
	var inventorySize int64
	for {
		header, err := archive.Next()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, io.EOF) {
			return errObjectBackupInventory
		}
		if err != nil || !validObjectBackupHeader(header) {
			return errObjectBackupCopy
		}
		if header.Name == objectInventoryName {
			inventorySize = header.Size
			break
		}
		path := strings.Split(header.Name, "/")
		if len(path) != 3 || path[0] != "objects" || !validObjectBackupID(path[1]) || !validObjectBackupID(path[2]) ||
			header.Name <= previous || header.Size > objectBackupMaxFile || len(observed) == objectInventoryMaxRows {
			return errObjectBackupCopy
		}
		digest := sha256.New()
		n, err := io.Copy(digest, archive)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || n != header.Size {
			return errObjectBackupCopy
		}
		observed[header.Name] = objectBackupPayload{n, hex.EncodeToString(digest.Sum(nil))}
		previous = header.Name
	}
	if inventorySize > objectInventoryMaxBytes {
		return errObjectBackupInventory
	}
	raw, err := io.ReadAll(archive)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || int64(len(raw)) != inventorySize {
		return errObjectBackupInventory
	}
	read := input.bytes
	padding := (tarBlockSize - inventorySize%tarBlockSize) % tarBlockSize
	if _, err := archive.Next(); err != io.EOF || input.bytes-read != padding+2*tarBlockSize {
		return errObjectBackupCopy
	}
	var trailing [1]byte
	if _, err := io.ReadFull(input, trailing[:]); err != io.EOF || input.bytes != pin.CopySizeBytes ||
		hex.EncodeToString(hash.Sum(nil)) != pin.CopySHA256 || !unchangedObjectBackupFile(root, objectBackupCopyName, file, info) {
		return errObjectBackupCopy
	}
	return checkObjectBackupInventory(ctx, raw, pin, observed)
}
