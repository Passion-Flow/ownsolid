// Package maintenance exposes fixed M17 owning operations to the control worker.
// It neither invokes a CLI nor replaces the original scope journal or attestation.
package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Command string

const (
	Backup                 Command = "backup"
	InspectBackup          Command = "inspect-backup"
	ReconcileBackup        Command = "reconcile-backup"
	InspectRestoreSource   Command = "inspect-restore-source"
	InspectRestoreTarget   Command = "inspect-restore-target"
	ReserveRestore         Command = "prepare-restore"
	ReconcileReservation   Command = "reconcile-restore-reservation"
	RestoreSQL             Command = "restore-sql"
	ReconcileSQL           Command = "reconcile-restore-sql"
	VerifyRestoredDatabase Command = "qualify-restored-database"
	ReconcileDatabase      Command = "reconcile-restored-database"
	RestoreStores          Command = "restore-stores"
	ReconcileStores        Command = "reconcile-restored-stores"
)

// These aliases retain the original validated request format and defaults. Only
// trusted server configuration constructs them; HTTP clients select opaque keys.
type BackupRequest = configuration.BackupRequest
type RestoreRequest = configuration.RestoreRequest
type RestoreSourceRequest = configuration.RestoreSourceRequest
type DatabaseSource = engine.PostgresBackupSource

func LoadBackupRequest(raw, environment []byte) (BackupRequest, error) {
	return configuration.LoadBackupRequest(raw, environment)
}

func BackupDefaults() BackupRequest { return configuration.BackupDefaults() }
func RestoreDefaults() RestoreRequest {
	return RestoreRequest{RestoreSourceRequest: configuration.RestoreSourceDefaults()}
}

type Configuration struct {
	value       configuration.Config
	fingerprint string
}

var scopeIdentity = regexp.MustCompile(`^[a-f0-9]{32}$`)

// LoadBound is mandatory for registered maintenance operations. The same
// expected identity reaches each original owner's actual descriptor/lock open.
func LoadBound(raw, environment []byte, expectedScopeID string) (*Configuration, error) {
	if !scopeIdentity.MatchString(expectedScopeID) {
		return nil, errors.New("MAINTENANCE_REGISTERED_SCOPE_BINDING_REQUIRED")
	}
	c, e := Load(raw, environment)
	if e != nil {
		return nil, e
	}
	c.value.BoundScopeID = expectedScopeID
	return c, nil
}

func digest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

// Load uses the existing strict typed loader. No ambient environment is read;
// callers must read their protected preconfigured input through its owning loader.
func Load(raw, environment []byte) (*Configuration, error) {
	c, e := configuration.Load(raw, environment)
	if e != nil {
		return nil, e
	}
	if c.ScopeRoot == "" || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil {
		return nil, errors.New("MAINTENANCE_INSTALLED_SCOPE_CONFIGURATION_REQUIRED")
	}
	// Length-prefix through JSON, rather than ambiguous concatenated bytes.
	bound, _ := json.Marshal(struct{ Configuration, Environment []byte }{raw, environment})
	return &Configuration{value: c, fingerprint: digest(bound)}, nil
}

func (c *Configuration) Fingerprint() string {
	if c == nil {
		return ""
	}
	return c.fingerprint
}

// ScopeRoot is trusted composition information, never a browser response field.
func (c *Configuration) ScopeRoot() string {
	if c == nil {
		return ""
	}
	return c.value.ScopeRoot
}

type Request struct {
	Backup  *BackupRequest
	Restore *RestoreRequest
	Source  *RestoreSourceRequest
}

// Result keeps exact original status/qualification and original-result hash.
// Pending is an unresolved stage, not a successful completed operation. Original
// remains available only to the private evidence owner, not JSON HTTP projection.
type Result struct {
	Command               Command                    `json:"command"`
	Status                string                     `json:"status"`
	ScopeID               string                     `json:"scope_id,omitempty"`
	AttemptID             string                     `json:"attempt_id,omitempty"`
	Pending               bool                       `json:"pending"`
	CompletedSteps        int                        `json:"completed_steps,omitempty"`
	TotalSteps            int                        `json:"total_steps,omitempty"`
	ManifestSHA256        string                     `json:"manifest_sha256,omitempty"`
	ProofSHA256           string                     `json:"proof_sha256,omitempty"`
	Qualification         string                     `json:"qualification"`
	EvidenceSHA256        string                     `json:"evidence_sha256"`
	Original              json.RawMessage            `json:"-"`
	SourceScopeID         string                     `json:"source_scope_id,omitempty"`
	Profile               string                     `json:"profile,omitempty"`
	ReleaseManifestSHA256 string                     `json:"release_manifest_sha256,omitempty"`
	NativePostgreSQL      *NativePostgreSQLReference `json:"native_postgresql,omitempty"`
	Source                *DatabaseSource            `json:"source,omitempty"`
	PlanSHA256            string                     `json:"plan_sha256,omitempty"`
	Stores                []string                   `json:"stores,omitempty"`
}

type Progress struct {
	ScopeID     string  `json:"scope_id"`
	Backup      *Result `json:"backup,omitempty"`
	Reservation *Result `json:"reservation,omitempty"`
	SQL         *Result `json:"sql,omitempty"`
	Database    *Result `json:"database,omitempty"`
	Stores      *Result `json:"stores,omitempty"`
}

// ReadProgress reads the original actual journal/proofs under its owning lock.
// Missing stages stay missing; this call creates no intent or execution effect.
func ReadProgress(ctx context.Context, c *Configuration) (Progress, error) {
	if c == nil || !scopeIdentity.MatchString(c.value.BoundScopeID) {
		return Progress{}, errors.New("MAINTENANCE_REGISTERED_SCOPE_BINDING_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return Progress{}, errors.New("MAINTENANCE_OPERATION_DEADLINE_REQUIRED")
	}
	s, e := scope.Read(ctx, c.value)
	if e != nil {
		return Progress{}, e
	}
	out := Progress{ScopeID: s.ScopeID}
	for _, entry := range []struct {
		command     Command
		original    any
		destination **Result
	}{{Backup, s.Backup, &out.Backup}, {ReserveRestore, s.Restore, &out.Reservation}, {RestoreSQL, s.RestoreSQL, &out.SQL}, {VerifyRestoredDatabase, s.RestoreDatabase, &out.Database}, {RestoreStores, s.RestoreStores, &out.Stores}} {
		raw, _ := json.Marshal(entry.original)
		if string(raw) == "null" {
			continue
		}
		r, e := project(entry.command, entry.original)
		if e != nil {
			return Progress{}, e
		}
		*entry.destination = &r
	}
	return out, nil
}

func requestKind(command Command) string {
	switch command {
	case Backup, InspectBackup, ReconcileBackup:
		return "backup"
	case InspectRestoreSource, InspectRestoreTarget:
		return "source"
	case ReserveRestore, ReconcileReservation, RestoreSQL, ReconcileSQL, VerifyRestoredDatabase, ReconcileDatabase, RestoreStores, ReconcileStores:
		return "restore"
	default:
		return ""
	}
}

func (r Request) validate(command Command) error {
	kind := requestKind(command)
	if kind == "" || (r.Backup != nil) != (kind == "backup") || (r.Restore != nil) != (kind == "restore") || (r.Source != nil) != (kind == "source") {
		return errors.New("MAINTENANCE_FIXED_COMMAND_REQUEST_REQUIRED")
	}
	switch kind {
	case "backup":
		return r.Backup.Validate()
	case "restore":
		return r.Restore.Validate()
	case "source":
		return r.Source.Validate()
	}
	return errors.New("MAINTENANCE_FIXED_COMMAND_REQUEST_REQUIRED")
}

// Run performs one exact original stage. It never retries, advances another
// stage, interprets a failed partial result as success, or manufactures proof.
func Run(ctx context.Context, c *Configuration, command Command, request Request) (Result, error) {
	if c == nil || c.fingerprint == "" {
		return Result{}, errors.New("MAINTENANCE_INSTALLED_SCOPE_CONFIGURATION_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return Result{}, errors.New("MAINTENANCE_OPERATION_DEADLINE_REQUIRED")
	}
	if e := request.validate(command); e != nil {
		return Result{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.value.TimeoutSeconds)*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return Result{}, e
	}
	var original any
	var e error
	switch command {
	case Backup, InspectBackup, ReconcileBackup:
		original, e = scope.RunBackup(ctx, c.value, *request.Backup, string(command))
	case InspectRestoreSource:
		original, e = scope.InspectRestoreSource(ctx, c.value, *request.Source)
	case InspectRestoreTarget:
		original, e = scope.InspectRestoreTarget(ctx, c.value, *request.Source)
	case ReserveRestore, ReconcileReservation:
		original, e = scope.RunRestoreReservation(ctx, c.value, *request.Restore, string(command))
	case RestoreSQL, ReconcileSQL:
		original, e = scope.RunRestoreSQL(ctx, c.value, *request.Restore, string(command))
	case VerifyRestoredDatabase, ReconcileDatabase:
		original, e = scope.RunRestoreDatabase(ctx, c.value, *request.Restore, string(command))
	case RestoreStores, ReconcileStores:
		original, e = scope.RunRestoreStores(ctx, c.value, *request.Restore, string(command))
	default:
		return Result{}, errors.New("MAINTENANCE_FIXED_COMMAND_REQUEST_REQUIRED")
	}
	if e != nil {
		return Result{}, e
	}
	return project(command, original)
}

func project(command Command, original any) (Result, error) {
	raw, e := json.Marshal(original)
	if e != nil {
		return Result{}, errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
	}
	var common struct {
		Status                string          `json:"status"`
		ScopeID               string          `json:"scope_id"`
		AttemptID             string          `json:"attempt_id"`
		Pending               bool            `json:"pending"`
		CompletedSteps        int             `json:"completed_steps"`
		TotalSteps            int             `json:"total_steps"`
		ManifestSHA256        string          `json:"manifest_sha256"`
		ProofSHA256           string          `json:"proof_sha256"`
		Qualification         string          `json:"qualification"`
		SourceScopeID         string          `json:"source_scope_id"`
		Profile               string          `json:"profile"`
		ReleaseManifestSHA256 string          `json:"release_manifest_sha256"`
		NativePostgreSQL      json.RawMessage `json:"native_postgresql"`
		Source                *DatabaseSource `json:"source"`
		PlanSHA256            string          `json:"plan_sha256"`
		Stores                []string        `json:"stores"`
	}
	if wire.UniqueJSON(raw, &common) != nil || common.Status == "" || common.Qualification == "" || common.CompletedSteps < 0 || common.TotalSteps < common.CompletedSteps {
		return Result{}, errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
	}
	var native *NativePostgreSQLReference
	if len(common.NativePostgreSQL) != 0 {
		var reference NativePostgreSQLReference
		if reference.UnmarshalJSON(common.NativePostgreSQL) != nil {
			return Result{}, errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
		}
		native = &reference
	}
	result := Result{Command: command, Status: common.Status, ScopeID: common.ScopeID, AttemptID: common.AttemptID, Pending: common.Pending, CompletedSteps: common.CompletedSteps, TotalSteps: common.TotalSteps, ManifestSHA256: common.ManifestSHA256, ProofSHA256: common.ProofSHA256, Qualification: common.Qualification, EvidenceSHA256: digest(raw), Original: raw, SourceScopeID: common.SourceScopeID, Profile: common.Profile, ReleaseManifestSHA256: common.ReleaseManifestSHA256, NativePostgreSQL: native, Source: common.Source, PlanSHA256: common.PlanSHA256, Stores: append([]string(nil), common.Stores...)}
	if !validNativeProjection(result) {
		return Result{}, errors.New("MAINTENANCE_ORIGINAL_RESULT_INVALID")
	}
	return result, nil
}
