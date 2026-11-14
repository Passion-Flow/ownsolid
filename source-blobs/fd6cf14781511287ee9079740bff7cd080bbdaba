package release

import (
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
)

const PostgreSQLRestoreTargetInitializeCommand = "initialize-kubernetes-postgres-restore-target"
const PostgreSQLRestoreTargetInspectCommand = "inspect-kubernetes-postgres-restore-target"
const PostgreSQLRestoreTargetObservationFormat = "projectone.postgres.restore-target-observation.v1"

// The Scope/Engine owners independently verify these original reservation and
// container facts. Repeating them in a SQL observation does not grant authority
// over Docker, physical writers, a signed release or a different reservation.
type PostgreSQLRestoreTargetInitializationBinding struct {
	RestoreAttemptID       string `json:"restore_attempt_id"`
	ReservationPlanSHA256  string `json:"reservation_plan_sha256"`
	TargetDescriptorSHA256 string `json:"target_descriptor_sha256"`
	TargetRuntimeSHA256    string `json:"target_runtime_sha256"`
	SourceCandidateSHA256  string `json:"source_candidate_sha256"`
	NetworkID              string `json:"network_id"`
	PostgresContainerID    string `json:"postgres_container_id"`
}

type PostgreSQLRestoreTargetInitializationEndpoint struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	SSLMode string `json:"sslmode"`
}

type PostgreSQLRestoreTargetInitializationPolicy struct {
	OperationSeconds   int   `json:"operation_seconds"`
	CleanupSeconds     int   `json:"cleanup_seconds"`
	MaximumRecordBytes int64 `json:"maximum_record_bytes"`
	MinimumFreeBytes   int64 `json:"minimum_free_bytes"`
}

func (p PostgreSQLRestoreTargetInitializationPolicy) Valid() bool {
	return p.OperationSeconds >= 1 && p.OperationSeconds <= 300 && p.CleanupSeconds >= 1 && p.CleanupSeconds <= 30 && p.CleanupSeconds <= p.OperationSeconds && p.MaximumRecordBytes >= 4096 && p.MaximumRecordBytes <= 1<<20 && p.MinimumFreeBytes >= 1<<20 && p.MinimumFreeBytes <= 1<<40
}

// A zero pin tuple is allowed only for readonly discovery. Initialization always
// consumes a separately frozen, fully pinned request after that observation.
// The target database already exists; this operation creates no database/role.
type PostgreSQLRestoreTargetInitializationRequest struct {
	FormatVersion            int                                           `json:"format_version"`
	ApplicationProfile       string                                        `json:"application_profile"`
	InitializationID         string                                        `json:"initialization_id"`
	TargetScopeID            string                                        `json:"target_scope_id"`
	Binding                  PostgreSQLRestoreTargetInitializationBinding  `json:"binding"`
	SourceDatabase           string                                        `json:"source_database"`
	TargetDatabase           string                                        `json:"target_database"`
	TargetOperator           string                                        `json:"target_operator"`
	Endpoint                 PostgreSQLRestoreTargetInitializationEndpoint `json:"endpoint"`
	ExpectedSystemIdentifier string                                        `json:"expected_system_identifier"`
	ExpectedDatabaseOID      uint32                                        `json:"expected_database_oid"`
	InitializationRoot       string                                        `json:"initialization_root"`
	Policy                   PostgreSQLRestoreTargetInitializationPolicy   `json:"policy"`
}

func (r PostgreSQLRestoreTargetInitializationRequest) IdentityPinned() bool {
	n, e := strconv.ParseUint(r.ExpectedSystemIdentifier, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == r.ExpectedSystemIdentifier && r.ExpectedDatabaseOID > 0
}

func (r PostgreSQLRestoreTargetInitializationRequest) ValidForInspection() bool {
	b, ep := r.Binding, r.Endpoint
	if r.FormatVersion != 1 || r.ApplicationProfile != MVPPostgreSQLCPUApplicationProfile || !restoreGeneration.MatchString(r.InitializationID) || !restoreGeneration.MatchString(r.TargetScopeID) || r.InitializationID == r.TargetScopeID || !restoreGeneration.MatchString(b.RestoreAttemptID) || !PostgreSQLBackupPath(r.InitializationRoot) || !r.Policy.Valid() || !postgresBackupSQLName.MatchString(r.SourceDatabase) || !postgresBackupSQLName.MatchString(r.TargetDatabase) || r.SourceDatabase == r.TargetDatabase || !postgresBackupSQLName.MatchString(r.TargetOperator) {
		return false
	}
	if r.TargetDatabase == "postgres" || r.TargetDatabase == "template0" || r.TargetDatabase == "template1" {
		return false
	}
	if ep.Host == "" || len(ep.Host) > 253 || strings.TrimSpace(ep.Host) != ep.Host || strings.ContainsAny(ep.Host, " /\\\x00\r\n\t%@,=") || ep.Port < 1 || ep.Port > 65535 || ep.SSLMode != "disable" && ep.SSLMode != "verify-full" {
		return false
	}
	if strings.ContainsAny(ep.Host, ":[]") && (net.ParseIP(ep.Host) == nil || strings.ContainsAny(ep.Host, "[]")) {
		return false
	}
	for _, sha := range []string{b.ReservationPlanSHA256, b.TargetDescriptorSHA256, b.TargetRuntimeSHA256, b.SourceCandidateSHA256, b.NetworkID, b.PostgresContainerID} {
		if !ValidSHA256(sha) {
			return false
		}
	}
	return r.IdentityPinned() || r.ExpectedSystemIdentifier == "" && r.ExpectedDatabaseOID == 0
}

func (r PostgreSQLRestoreTargetInitializationRequest) Valid() bool {
	return r.ValidForInspection() && r.IdentityPinned()
}

func (r PostgreSQLRestoreTargetInitializationRequest) Marker() string {
	return "projectone:kubernetes-restore:" + r.TargetScopeID
}

func (r PostgreSQLRestoreTargetInitializationRequest) SHA256() string {
	raw, e := json.Marshal(r)
	if e != nil || !r.ValidForInspection() {
		return ""
	}
	return Digest(append(raw, '\n'))
}

// This identity has the original native RestoreTarget wire shape. PID is an
// observation fact, excluded from the identity digest when a new PID reopens it.
type PostgreSQLRestoreTargetInitializationIdentity struct {
	SystemIdentifier string `json:"system_identifier"`
	DatabaseOID      uint32 `json:"database_oid"`
	Database         string `json:"database"`
	Operator         string `json:"operator"`
	BackendPID       int    `json:"backend_pid"`
	ServerVersion    int    `json:"server_version"`
	Marker           string `json:"marker"`
}

func (t PostgreSQLRestoreTargetInitializationIdentity) SHA256() string {
	t.BackendPID = 0
	raw, e := json.Marshal(t)
	if e != nil {
		return ""
	}
	return Digest(append(raw, '\n'))
}

type PostgreSQLRestoreTargetInitializationObservation struct {
	Format                    string                                        `json:"format"`
	RequestSHA256             string                                        `json:"request_sha256"`
	InitializationID          string                                        `json:"initialization_id"`
	TargetScopeID             string                                        `json:"target_scope_id"`
	Binding                   PostgreSQLRestoreTargetInitializationBinding  `json:"binding"`
	Target                    PostgreSQLRestoreTargetInitializationIdentity `json:"target"`
	TargetBindingSHA256       string                                        `json:"target_binding_sha256"`
	PristinePredicateSHA256   string                                        `json:"pristine_predicate_sha256"`
	IntentSHA256              string                                        `json:"intent_sha256"`
	CompletionSHA256          string                                        `json:"completion_sha256"`
	ReadOnly                  bool                                          `json:"read_only"`
	IdentityPinned            bool                                          `json:"identity_pinned"`
	OriginalIntentPresent     bool                                          `json:"original_intent_present"`
	OriginalCompletionPresent bool                                          `json:"original_completion_present"`
	EffectObserved            bool                                          `json:"effect_observed"`
	State                     string                                        `json:"state"`
	CurrentLockHeld           bool                                          `json:"current_lock_held"`
	Qualification             string                                        `json:"qualification"`
}

func (o PostgreSQLRestoreTargetInitializationObservation) ValidFor(r PostgreSQLRestoreTargetInitializationRequest, inspection bool) bool {
	t := o.Target
	n, e := strconv.ParseUint(t.SystemIdentifier, 10, 64)
	if !r.ValidForInspection() || !inspection && !r.Valid() || o.Format != PostgreSQLRestoreTargetObservationFormat || o.RequestSHA256 != r.SHA256() || o.InitializationID != r.InitializationID || o.TargetScopeID != r.TargetScopeID || o.Binding != r.Binding || o.ReadOnly != inspection || o.IdentityPinned != r.IdentityPinned() || o.CurrentLockHeld || !ValidSHA256(o.PristinePredicateSHA256) || o.TargetBindingSHA256 != t.SHA256() || e != nil || n == 0 || strconv.FormatUint(n, 10) != t.SystemIdentifier || t.DatabaseOID == 0 || t.BackendPID < 1 || t.ServerVersion != 180006 || t.Database != r.TargetDatabase || t.Operator != r.TargetOperator || o.Qualification == "" {
		return false
	}
	if r.IdentityPinned() && (t.SystemIdentifier != r.ExpectedSystemIdentifier || t.DatabaseOID != r.ExpectedDatabaseOID) {
		return false
	}
	if o.OriginalIntentPresent != ValidSHA256(o.IntentSHA256) || !o.OriginalIntentPresent && o.IntentSHA256 != "" || o.OriginalCompletionPresent != ValidSHA256(o.CompletionSHA256) || !o.OriginalCompletionPresent && o.CompletionSHA256 != "" || o.OriginalCompletionPresent && !o.OriginalIntentPresent {
		return false
	}
	switch o.State {
	case "uninitialized":
		return inspection && !o.OriginalIntentPresent && !o.EffectObserved && t.Marker == ""
	case "effect_not_observed":
		return inspection && r.IdentityPinned() && o.OriginalIntentPresent && !o.OriginalCompletionPresent && !o.EffectObserved && t.Marker == ""
	case "initialized":
		return r.IdentityPinned() && o.OriginalIntentPresent && o.EffectObserved && t.Marker == r.Marker() && (inspection || o.OriginalCompletionPresent)
	}
	return false
}

// All fields are mandatory, including explicit false/empty discovery fields.
// Case aliases, duplicate/null/unknown/missing fields refuse at every depth.
func decodeRestoreTargetInitializationJSON(raw []byte, value any) error {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JSON_INVALID")
	var exact func([]byte, reflect.Type) error
	exact = func(body []byte, t reflect.Type) error {
		if t.Kind() != reflect.Struct {
			return nil
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) != nil || len(fields) != t.NumField() {
			return bad
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := f.Tag.Get("json")
			member, ok := fields[name]
			if !ok || exact(member, f.Type) != nil {
				return bad
			}
		}
		return nil
	}
	if StrictJSON(raw, value) != nil || exact(raw, reflect.TypeOf(value).Elem()) != nil {
		return bad
	}
	return nil
}

func (r *PostgreSQLRestoreTargetInitializationRequest) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreTargetInitializationRequest
	var v plain
	if r == nil || decodeRestoreTargetInitializationJSON(raw, &v) != nil {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JSON_INVALID")
	}
	*r = PostgreSQLRestoreTargetInitializationRequest(v)
	return nil
}

func (o *PostgreSQLRestoreTargetInitializationObservation) UnmarshalJSON(raw []byte) error {
	type plain PostgreSQLRestoreTargetInitializationObservation
	var v plain
	if o == nil || decodeRestoreTargetInitializationJSON(raw, &v) != nil {
		return errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_JSON_INVALID")
	}
	*o = PostgreSQLRestoreTargetInitializationObservation(v)
	return nil
}
