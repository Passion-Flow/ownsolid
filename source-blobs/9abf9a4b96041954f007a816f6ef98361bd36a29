package release

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// PostgreSQLRestoreRoot is a measured directory identity, never configuration.
// The slice order is models, datasets, training, model preparations.
type PostgreSQLRestoreRoot struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}

func ValidPostgreSQLRestoreRoots(roots []PostgreSQLRestoreRoot) bool {
	if roots == nil {
		return true // Preserve the original standalone request protocol.
	}
	if len(roots) != 4 {
		return false
	}
	for i, root := range roots {
		if !PostgreSQLBackupPath(root.Path) || root.Device == 0 || root.Inode == 0 || root.UID == 0 || root.GID == 0 || root.UID > 2147483647 || root.GID > 2147483647 {
			return false
		}
		for _, prior := range roots[:i] {
			if root.Path == prior.Path || strings.HasPrefix(root.Path, prior.Path+"/") || strings.HasPrefix(prior.Path, root.Path+"/") || root.Device == prior.Device && root.Inode == prior.Inode {
				return false
			}
		}
	}
	return true
}

// encoding/json otherwise accepts case-insensitive aliases. Check canonical
// names in addition to the shared duplicate/null/unknown-field boundary.
func canonicalRestoreObject(raw []byte, value any, required bool) error {
	bad := errors.New("POSTGRES_RESTORE_REQUEST_JSON_INVALID")
	var fields map[string]json.RawMessage
	if StrictJSON(raw, value) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		return bad
	}
	shape := reflect.TypeOf(value).Elem()
	allowed := make(map[string]bool, shape.NumField())
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		allowed[name] = true
	}
	for name := range fields {
		if !allowed[name] {
			return bad
		}
	}
	if required && len(fields) != len(allowed) {
		return bad
	}
	return nil
}

func (r *PostgreSQLRestoreRoot) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreRoot
	var value plain
	if r == nil {
		return errors.New("POSTGRES_RESTORE_REQUEST_JSON_INVALID")
	}
	if e := canonicalRestoreObject(raw, &value, true); e != nil {
		return e
	}
	*r = PostgreSQLRestoreRoot(value)
	return nil
}

func (r *PostgreSQLRestoreRequest) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreRequest
	var value plain
	if r == nil {
		return errors.New("POSTGRES_RESTORE_REQUEST_JSON_INVALID")
	}
	if e := canonicalRestoreObject(raw, &value, false); e != nil {
		return e
	}
	if !validPostgreSQLApplicationJSON(raw, value.ApplicationProfile) {
		return errors.New("POSTGRES_RESTORE_REQUEST_JSON_INVALID")
	}
	if !ValidPostgreSQLRestoreRoots(value.TargetRoots) {
		return errors.New("POSTGRES_RESTORE_TARGET_ROOTS_INVALID")
	}
	*r = PostgreSQLRestoreRequest(value)
	return nil
}
