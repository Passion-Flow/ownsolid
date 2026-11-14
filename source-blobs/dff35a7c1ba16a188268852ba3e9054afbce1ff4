package release

import (
	"encoding/json"
	"errors"
	"time"
)

const PostgreSQLRestoreRootsInspectCommand = "inspect-kubernetes-postgres-restore-roots"
const PostgreSQLRestoreRootsInspectionFormat = "projectone.postgres.restore-roots-observation.v1"
const PostgreSQLRestoreRootsInspectionQualification = "Original readonly instant observation of four empty private target directories; locks are released. Scope physical custody and final native Restore relocking/comparison remain required."

type PostgreSQLRestoreRootsInspectionPolicy struct {
	OperationSeconds   int   `json:"operation_seconds"`
	MaximumRecordBytes int64 `json:"maximum_record_bytes"`
}

func (p PostgreSQLRestoreRootsInspectionPolicy) Valid() bool {
	return p.OperationSeconds >= 1 && p.OperationSeconds <= 300 && p.MaximumRecordBytes >= 4096 && p.MaximumRecordBytes <= 1<<20
}

// Paths come only from the original protected typed runtime, never a caller's
// arbitrary list. This request precedes the final native restore request/pins.
type PostgreSQLRestoreRootsInspectionRequest struct {
	FormatVersion              int                                          `json:"format_version"`
	ApplicationProfile         string                                       `json:"application_profile"`
	InspectionID               string                                       `json:"inspection_id"`
	TargetScopeID              string                                       `json:"target_scope_id"`
	Binding                    PostgreSQLRestoreTargetInitializationBinding `json:"binding"`
	RuntimeConfigurationRoot   string                                       `json:"runtime_configuration_root"`
	RuntimeConfigurationSHA256 string                                       `json:"runtime_configuration_sha256"`
	RuntimeEnvironmentSHA256   string                                       `json:"runtime_environment_sha256"`
	ExpectedUID                uint32                                       `json:"expected_uid"`
	ExpectedGID                uint32                                       `json:"expected_gid"`
	Policy                     PostgreSQLRestoreRootsInspectionPolicy       `json:"policy"`
}

func (r PostgreSQLRestoreRootsInspectionRequest) Valid() bool {
	if r.FormatVersion != 1 || r.ApplicationProfile != MVPPostgreSQLCPUApplicationProfile || !restoreGeneration.MatchString(r.InspectionID) || !restoreGeneration.MatchString(r.TargetScopeID) || r.InspectionID == r.TargetScopeID || r.InspectionID == r.Binding.RestoreAttemptID || !restoreGeneration.MatchString(r.Binding.RestoreAttemptID) || !PostgreSQLBackupPath(r.RuntimeConfigurationRoot) || !ValidSHA256(r.RuntimeConfigurationSHA256) || r.RuntimeEnvironmentSHA256 != "" && !ValidSHA256(r.RuntimeEnvironmentSHA256) || r.ExpectedUID == 0 || r.ExpectedUID > 2147483647 || r.ExpectedGID == 0 || r.ExpectedGID > 2147483647 || !r.Policy.Valid() {
		return false
	}
	b := r.Binding
	for _, sha := range []string{b.ReservationPlanSHA256, b.TargetDescriptorSHA256, b.TargetRuntimeSHA256, b.SourceCandidateSHA256, b.NetworkID, b.PostgresContainerID} {
		if !ValidSHA256(sha) {
			return false
		}
	}
	return true
}

func (r PostgreSQLRestoreRootsInspectionRequest) SHA256() string {
	if !r.Valid() {
		return ""
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return ""
	}
	return Digest(append(raw, '\n'))
}

func PostgreSQLRestoreRootsSHA256(roots []PostgreSQLRestoreRoot) string {
	if len(roots) != 4 || !ValidPostgreSQLRestoreRoots(roots) {
		return ""
	}
	raw, e := json.Marshal(roots)
	if e != nil {
		return ""
	}
	return Digest(append(raw, '\n'))
}

type PostgreSQLRestoreRootsInspectionObservation struct {
	Format                       string                                       `json:"format"`
	RequestSHA256                string                                       `json:"request_sha256"`
	InspectionID                 string                                       `json:"inspection_id"`
	TargetScopeID                string                                       `json:"target_scope_id"`
	Binding                      PostgreSQLRestoreTargetInitializationBinding `json:"binding"`
	RuntimeConfigurationSHA256   string                                       `json:"runtime_configuration_sha256"`
	RuntimeEnvironmentSHA256     string                                       `json:"runtime_environment_sha256"`
	EffectiveConfigurationSHA256 string                                       `json:"effective_configuration_sha256"`
	Roots                        []PostgreSQLRestoreRoot                      `json:"roots"`
	RootsSHA256                  string                                       `json:"roots_sha256"`
	ReadOnly                     bool                                         `json:"read_only"`
	CurrentLockHeld              bool                                         `json:"current_lock_held"`
	ObservedAt                   string                                       `json:"observed_at"`
	Qualification                string                                       `json:"qualification"`
}

func (o PostgreSQLRestoreRootsInspectionObservation) ValidFor(r PostgreSQLRestoreRootsInspectionRequest) bool {
	t, e := time.Parse(time.RFC3339Nano, o.ObservedAt)
	if !r.Valid() || o.Format != PostgreSQLRestoreRootsInspectionFormat || o.RequestSHA256 != r.SHA256() || o.InspectionID != r.InspectionID || o.TargetScopeID != r.TargetScopeID || o.Binding != r.Binding || o.RuntimeConfigurationSHA256 != r.RuntimeConfigurationSHA256 || o.RuntimeEnvironmentSHA256 != r.RuntimeEnvironmentSHA256 || !ValidSHA256(o.EffectiveConfigurationSHA256) || len(o.Roots) != 4 || !ValidPostgreSQLRestoreRoots(o.Roots) || o.RootsSHA256 != PostgreSQLRestoreRootsSHA256(o.Roots) || !o.ReadOnly || o.CurrentLockHeld || e != nil || t.UTC().Format(time.RFC3339Nano) != o.ObservedAt || o.Qualification != PostgreSQLRestoreRootsInspectionQualification {
		return false
	}
	for _, root := range o.Roots {
		if root.UID != r.ExpectedUID || root.GID != r.ExpectedGID {
			return false
		}
	}
	return true
}

// The existing strict boundary rejects duplicate/null fields. Canonical object
// inventories also reject aliases/missing fields, including nested bindings.
func decodeRestoreRootsInspection(raw []byte, value any) error {
	bad := errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_JSON_INVALID")
	if canonicalRestoreObject(raw, value, true) != nil {
		return bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return bad
	}
	var binding PostgreSQLRestoreTargetInitializationBinding
	if canonicalRestoreObject(fields["binding"], &binding, true) != nil {
		return bad
	}
	return nil
}

func (p *PostgreSQLRestoreRootsInspectionPolicy) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreRootsInspectionPolicy
	var value plain
	if p == nil || canonicalRestoreObject(raw, &value, true) != nil {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_JSON_INVALID")
	}
	*p = PostgreSQLRestoreRootsInspectionPolicy(value)
	return nil
}

func (r *PostgreSQLRestoreRootsInspectionRequest) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreRootsInspectionRequest
	var value plain
	if r == nil || decodeRestoreRootsInspection(raw, &value) != nil {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_JSON_INVALID")
	}
	*r = PostgreSQLRestoreRootsInspectionRequest(value)
	return nil
}

func (o *PostgreSQLRestoreRootsInspectionObservation) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreRootsInspectionObservation
	var value plain
	if o == nil || decodeRestoreRootsInspection(raw, &value) != nil {
		return errors.New("POSTGRES_RESTORE_ROOTS_INSPECTION_JSON_INVALID")
	}
	*o = PostgreSQLRestoreRootsInspectionObservation(value)
	return nil
}
