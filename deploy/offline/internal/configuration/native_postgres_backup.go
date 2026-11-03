package configuration

import (
	"encoding/json"
	"errors"
	"path"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The descriptor belongs to the existing protected operator-config snapshot.
// It supplies the original native owner's typed policy and explicit deployment
// paths; it grants no SQL, arbitrary command, host bind, volume or proof authority.
const NativePostgreSQLRecoveryDescriptorFile = "native-postgres.json"

type NativePostgreSQLRecoveryDescriptor struct {
	FormatVersion            int                                               `json:"format_version"`
	BackupTemplate           wire.PostgreSQLBackupRequest                      `json:"backup_template"`
	RestoreTemplate          *wire.PostgreSQLRestoreRequest                    `json:"restore_template,omitempty"`
	InitializationPolicy     *wire.PostgreSQLRestoreTargetInitializationPolicy `json:"initialization_policy,omitempty"`
	InitializationParentRoot string                                            `json:"initialization_parent_root,omitempty"`
	StoreRoots               [4]string                                         `json:"store_roots"`
	RegistryRoot             string                                            `json:"registry_root"`
	PreparedReleaseRoot      string                                            `json:"prepared_release_root"`
	RegistryScaffoldRoot     string                                            `json:"registry_scaffold_root"`
}

func (d *NativePostgreSQLRecoveryDescriptor) UnmarshalJSON(raw []byte) error {
	type plain NativePostgreSQLRecoveryDescriptor
	var v plain
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_BACKUP_DESCRIPTOR_INVALID")
	if d == nil || wire.StrictJSON(raw, &v) != nil {
		return bad
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || members == nil {
		return bad
	}
	fields := map[string]bool{}
	shape := reflect.TypeOf(v)
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		fields[name] = true
	}
	for name := range members {
		if !fields[name] {
			return bad
		}
	}
	// encoding/json silently truncates excess elements when decoding [4]string.
	// Validate the actual untrusted array before accepting the fixed native order.
	var roots []string
	if wire.StrictJSON(members["store_roots"], &roots) != nil || len(roots) != 4 {
		return bad
	}
	*d = NativePostgreSQLRecoveryDescriptor(v)
	return nil
}

func LoadNativePostgreSQLRecoveryDescriptor(raw []byte) (NativePostgreSQLRecoveryDescriptor, error) {
	var d NativePostgreSQLRecoveryDescriptor
	if wire.StrictJSON(raw, &d) != nil || d.Validate() != nil {
		return NativePostgreSQLRecoveryDescriptor{}, errors.New("OFFLINE_OPERATOR_NATIVE_BACKUP_DESCRIPTOR_INVALID")
	}
	return d, nil
}

func (d NativePostgreSQLRecoveryDescriptor) Validate() error {
	bad := errors.New("OFFLINE_OPERATOR_NATIVE_BACKUP_DESCRIPTOR_INVALID")
	r := d.BackupTemplate
	if d.FormatVersion != 1 || !r.Valid() || r.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || path.Base(r.BackupRoot) != r.BackupID || r.RuntimeEnvironmentFile != "" || r.RuntimeConfigurationFile != path.Join(r.ConfigurationRoot, "runtime.yaml") {
		return bad
	}
	roots := []string{r.ConfigurationRoot, path.Dir(r.BackupRoot), r.Policy.ScratchRoot}
	roots = append(roots, d.StoreRoots[:]...)
	roots = append(roots, d.RegistryRoot, d.PreparedReleaseRoot)
	for i, a := range roots {
		if !wire.PostgreSQLBackupPath(a) || strings.HasPrefix(a, "/Volumes/") {
			return bad
		}
		for _, b := range roots[:i] {
			if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
				return bad
			}
		}
	}
	if !wire.PostgreSQLBackupPath(d.RegistryScaffoldRoot) || !strings.HasPrefix(d.RegistryRoot, d.RegistryScaffoldRoot+"/") || !strings.HasPrefix(d.PreparedReleaseRoot, d.RegistryScaffoldRoot+"/") {
		return bad
	}
	for _, a := range roots[:len(roots)-2] {
		if a == d.RegistryScaffoldRoot || strings.HasPrefix(a, d.RegistryScaffoldRoot+"/") || strings.HasPrefix(d.RegistryScaffoldRoot, a+"/") {
			return bad
		}
	}
	if d.RestoreTemplate != nil {
		q := d.RestoreTemplate
		if !q.Valid() || q.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || !reflect.DeepEqual(q.Policy, r.Policy) || q.RuntimeConfigurationFile != r.RuntimeConfigurationFile || q.RuntimeEnvironmentFile != r.RuntimeEnvironmentFile || q.ConfigurationRoot != r.ConfigurationRoot || len(q.TargetRoots) != 0 || path.Base(q.RestoreRoot) != q.RestoreID || d.InitializationPolicy == nil || !d.InitializationPolicy.Valid() || !wire.PostgreSQLBackupPath(d.InitializationParentRoot) {
			return bad
		}
		extra := []string{path.Dir(q.RestoreRoot), d.InitializationParentRoot}
		for i, a := range extra {
			for _, b := range append(append([]string{}, roots...), extra[:i]...) {
				if a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
					return bad
				}
			}
		}
	} else if d.InitializationPolicy != nil || d.InitializationParentRoot != "" {
		return bad
	}
	return nil
}

// The template is only a pre-effect deployment input. The existing scope owns
// the new nonce, physical stop evidence and final immutable request publication.
// No original native request is ever edited after that publication.
func (d NativePostgreSQLRecoveryDescriptor) BindBackup(scopeID, attemptID, maintenanceSHA, signedSourceSHA, controlImageSHA, postgresImageSHA, reason string) (wire.PostgreSQLBackupRequest, error) {
	r := d.BackupTemplate
	if d.Validate() != nil || r.SourceScopeID != scopeID || r.SourceManifestSHA256 != signedSourceSHA || r.ControlImageSHA256 != controlImageSHA || r.PostgresImageSHA256 != postgresImageSHA || !wire.ValidSHA256(maintenanceSHA) {
		return wire.PostgreSQLBackupRequest{}, errors.New("OFFLINE_OPERATOR_NATIVE_BACKUP_SOURCE_BINDING_INVALID")
	}
	r.BackupID = attemptID
	r.BackupRoot = path.Join(path.Dir(r.BackupRoot), attemptID)
	r.MaintenanceSHA256 = maintenanceSHA
	r.Reason = reason
	if !r.Valid() {
		return wire.PostgreSQLBackupRequest{}, errors.New("OFFLINE_OPERATOR_NATIVE_BACKUP_SOURCE_BINDING_INVALID")
	}
	return r, nil
}
