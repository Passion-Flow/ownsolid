package scope

import (
	"context"
	"path"
	"reflect"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const mvpNativeRestorePreparationFile = "mvp-native-restore-preparation.json"
const mvpNativeRestoreExecutionFile = "mvp-native-restore-execution.json"

// This pre-effect plan contains identities and digests only. Protected payload
// bytes are reconstructed from the frozen inputs, never serialized here.
type mvpNativeRestorePreparation struct {
	FormatVersion         int                                              `json:"format_version"`
	ScopeID               string                                           `json:"scope_id"`
	AttemptID             string                                           `json:"attempt_id"`
	RestorePlanSHA256     string                                           `json:"restore_plan_sha256"`
	Initializer           string                                           `json:"initializer"`
	Descriptor            configuration.NativePostgreSQLRecoveryDescriptor `json:"descriptor"`
	SourceRequest         wire.PostgreSQLBackupRequest                     `json:"source_request"`
	SourceArchiveSHA256   string                                           `json:"source_archive_sha256"`
	Archive               engine.NativePostgreSQLArchiveProfile            `json:"archive"`
	RootsTask             engine.DatabaseTask                              `json:"roots_task"`
	OperatorPayloadSHA256 string                                           `json:"operator_payload_sha256"`
	RuntimePayloadSHA256  string                                           `json:"runtime_payload_sha256"`
}

type mvpNativeRestoreExecution struct {
	FormatVersion       int                           `json:"format_version"`
	PreparationSHA256   string                        `json:"preparation_sha256"`
	SourceArchiveSHA256 string                        `json:"source_archive_sha256"`
	NativeRequest       wire.PostgreSQLRestoreRequest `json:"native_request"`
	Initializer         string                        `json:"initializer"`
	Writer              engine.DatabaseTask           `json:"writer"`
	PreparationFiles    []InputFile                   `json:"preparation_files"`
	TemporaryOwners     []engine.Ownership            `json:"temporary_owners"`
}

type mvpNativeRestoreReceipt struct {
	FormatVersion int        `json:"format_version"`
	Stage         string     `json:"stage"`
	Step          int        `json:"step"`
	PhysicalID    string     `json:"physical_id,omitempty"`
	Execution     *InputFile `json:"execution,omitempty"`
	Completion    *InputFile `json:"completion,omitempty"`
	Stdout        *InputFile `json:"stdout,omitempty"`
	Observation   *InputFile `json:"observation,omitempty"`
}

func nativeRestoreReceiptName(id string) string { return "mvp-native-restore-step-" + id + ".json" }

func (l *locked) nativeRestoreInputs(ctx context.Context) (preparedInputs, compose.Model, restoreConnection, error) {
	in, out, _ := inputsState(l.events)
	if in == nil || out != "completed" {
		return preparedInputs{}, compose.Model{}, restoreConnection{}, failure("OFFLINE_SCOPE_RESTORE_SQL_INPUTS_REQUIRED")
	}
	p, e := l.readInputs(ctx, *in)
	if e != nil {
		return p, compose.Model{}, restoreConnection{}, e
	}
	m, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return p, m, restoreConnection{}, e
	}
	c, e := restoreConnections(p, m)
	return p, m, c, e
}

func (l *locked) makeMVPNativeRestorePreparation(ctx context.Context, plan RestorePlan) (mvpNativeRestorePreparation, error) {
	var out mvpNativeRestorePreparation
	source, e := readMVPNativeRestoreSource(ctx, l.config, plan.Request.RestoreSourceRequest)
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(source.Result, plan.Source) {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_SOURCE_CHANGED")
	}
	p, m, _, e := l.nativeRestoreInputs(ctx)
	if e != nil {
		return out, e
	}
	d, e := l.mvpNativeRestoreDescriptor(p, m, source.Plan)
	if e != nil {
		return out, e
	}
	projection, e := projectMVPNativeConfiguration(p, m, d)
	if e != nil {
		return out, e
	}
	stores, _, e := l.mvpNativeRestoreDomainMounts(m, d)
	if e != nil {
		return out, e
	}
	uid, gid, e := roleUser(m, "control-config")
	if e != nil {
		return out, e
	}
	runtime := runtimeState(l.events)
	if !runtime.completed || runtime.record == nil || wire.Digest(encode(*runtime.record)) != plan.Target.RuntimeSHA256 || p.record.DescriptorSHA256 != plan.Target.DescriptorSHA256 {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TARGET_CHANGED")
	}
	id, e := nonce()
	if e != nil {
		return out, e
	}
	raw, e := restoreNamedFile(projection.ConfigurationFiles, path.Base(d.RestoreTemplate.RuntimeConfigurationFile))
	if e != nil {
		return out, e
	}
	envSHA := ""
	if d.RestoreTemplate.RuntimeEnvironmentFile != "" {
		env, e := restoreNamedFile(projection.ConfigurationFiles, path.Base(d.RestoreTemplate.RuntimeEnvironmentFile))
		if e != nil {
			return out, e
		}
		envSHA = wire.Digest(env)
	}
	binding := wire.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: plan.AttemptID, ReservationPlanSHA256: wire.Digest(encode(plan)), TargetDescriptorSHA256: plan.Target.DescriptorSHA256, TargetRuntimeSHA256: plan.Target.RuntimeSHA256, SourceCandidateSHA256: source.Result.NativePostgreSQL.CandidateSHA256, NetworkID: runtime.record.NetworkID, PostgresContainerID: runtime.record.Services["postgres"].ID}
	probe, e := d.BindRestoreRootsInspection(configuration.NativePostgreSQLRestoreRootsBinding{ScopeID: l.marker.ScopeID, InspectionID: id, Binding: binding, RuntimeConfigurationSHA256: wire.Digest(raw), RuntimeEnvironmentSHA256: envSHA, UID: uid, GID: gid})
	if e != nil {
		return out, e
	}
	peers := map[string]string{}
	for role, v := range runtime.record.Services {
		peers[role] = v.ID
	}
	owner := func(op, role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: op, Role: role + "-" + op}
	}
	task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: id, Role: "database-" + wire.PostgreSQLRestoreRootsInspectCommand}, VolumeOwner: owner(id, engine.NativePostgreSQLRestoreRootsInputRole), Kind: wire.PostgreSQLRestoreRootsInspectCommand, Image: m.Services["control"].Image, UID: uid, GID: gid, NetworkID: runtime.record.NetworkID, Peers: peers, NativePostgreSQLRestoreRootsInspection: &engine.NativePostgreSQLRestoreRootsInspectionProfile{Request: probe, ConfigurationOwner: owner(plan.AttemptID, engine.NativePostgreSQLConfigurationRole), RuntimeConfigurationFile: d.RestoreTemplate.RuntimeConfigurationFile, RuntimeEnvironmentFile: d.RestoreTemplate.RuntimeEnvironmentFile, Stores: stores}}
	if e = engine.ValidateNativePostgreSQLRestoreRootsInspectionTask(task); e != nil {
		return out, e
	}
	return mvpNativeRestorePreparation{FormatVersion: 1, ScopeID: l.marker.ScopeID, AttemptID: plan.AttemptID, RestorePlanSHA256: wire.Digest(encode(plan)), Initializer: p.descriptor.ImageID, Descriptor: d, SourceRequest: source.Request, SourceArchiveSHA256: source.ArchiveSHA256, Archive: source.NativeArchiveProfile, RootsTask: task, OperatorPayloadSHA256: projection.OperatorSHA256, RuntimePayloadSHA256: projection.ConfigurationSHA256}, nil
}

func (l *locked) readMVPNativeRestorePreparation() (mvpNativeRestorePreparation, error) {
	var p mvpNativeRestorePreparation
	s := restoreSQLState(l.events)
	raw, e := regular(l.root, mvpNativeRestorePreparationFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || s.record == nil || s.record.Native == nil || wire.Digest(raw) != s.record.Native.ExecutionSHA256 || wire.StrictJSON(raw, &p) != nil || p.FormatVersion != 1 || p.ScopeID != l.marker.ScopeID || p.AttemptID != s.record.Native.OriginalRestoreID || p.RestorePlanSHA256 != s.record.PlanSHA256 || p.Descriptor.Validate() != nil || engine.ValidateNativePostgreSQLRestoreRootsInspectionTask(p.RootsTask) != nil || !wire.ValidSHA256(p.SourceArchiveSHA256) {
		return p, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_CHANGED")
	}
	return p, nil
}

func nativeRestoreEventRecord(e Event, stage string) (int, string, bool) {
	switch stage {
	case "sql":
		if e.RestoreSQL != nil && e.RestoreSQL.Native != nil {
			return e.RestoreSQL.Step, e.RestoreSQL.ResultSHA256, true
		}
	case "database":
		if e.RestoreDatabase != nil && e.RestoreDatabase.Native != nil {
			return e.RestoreDatabase.Step, e.RestoreDatabase.ResultSHA256, true
		}
	case "stores":
		if e.RestoreStores != nil && e.RestoreStores.Native != nil {
			return e.RestoreStores.Step, e.RestoreStores.ResultSHA256, true
		}
	}
	return 0, "", false
}
func (l *locked) nativeRestoreReceipt(stage string, step int) (mvpNativeRestoreReceipt, string, error) {
	var r mvpNativeRestoreReceipt
	for i := len(l.events) - 1; i >= 0; i-- {
		ev := l.events[i]
		n, sha, ok := nativeRestoreEventRecord(ev, stage)
		if !ok || n != step || ev.Outcome != "completed" {
			continue
		}
		id := ev.OperationID
		if ev.ReferenceOperationID != "" {
			id = ev.ReferenceOperationID
		}
		raw, e := regular(l.root, nativeRestoreReceiptName(id), l.config.Verification.MaximumMetadataBytes)
		if e != nil || wire.Digest(raw) != sha || wire.StrictJSON(raw, &r) != nil || r.FormatVersion != 1 || r.Stage != stage || r.Step != step {
			return r, id, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RECEIPT_CHANGED")
		}
		return r, id, nil
	}
	return r, "", failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_RECEIPT_REQUIRED")
}
func (l *locked) readMVPNativeRestoreExecution(ctx context.Context) (mvpNativeRestoreExecution, error) {
	var e mvpNativeRestoreExecution
	p, err := l.readMVPNativeRestorePreparation()
	if err != nil {
		return e, err
	}
	r, _, err := l.nativeRestoreReceipt("sql", 0)
	if err != nil || r.Execution == nil {
		return e, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_REQUIRED")
	}
	raw, err := regular(l.root, mvpNativeRestoreExecutionFile, l.config.Verification.MaximumMetadataBytes)
	if err != nil || r.Execution.Name != mvpNativeRestoreExecutionFile || r.Execution.Bytes != int64(len(raw)) || r.Execution.SHA256 != wire.Digest(raw) || wire.StrictJSON(raw, &e) != nil || e.FormatVersion != 1 || e.PreparationSHA256 != wire.Digest(encode(p)) || e.SourceArchiveSHA256 != p.SourceArchiveSHA256 || e.Initializer != p.Initializer || e.Writer.NativePostgreSQLRecovery == nil || !reflect.DeepEqual(e.Writer.NativePostgreSQLRecovery.RestoreRequest, &e.NativeRequest) || engine.ValidateNativePostgreSQLRecoveryTask(e.Writer) != nil || e.Writer.Kind != engine.NativePostgreSQLRestore {
		return e, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_CHANGED")
	}
	if len(e.PreparationFiles) != 12 {
		return e, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_CUSTODY_CHANGED")
	}
	seen := map[string]bool{}
	for _, file := range e.PreparationFiles {
		raw, readError := regular(l.root, file.Name, l.config.Verification.MaximumMetadataBytes)
		if readError != nil || seen[file.Name] || !strings.HasPrefix(file.Name, "mvp-native-prepare-") || int64(len(raw)) != file.Bytes || wire.Digest(raw) != file.SHA256 {
			return e, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_CUSTODY_CHANGED")
		}
		seen[file.Name] = true
	}
	plan, err := l.readBoundRestorePlan()
	if err != nil {
		return e, err
	}
	_, _, c, err := l.nativeRestoreInputs(ctx)
	if err != nil {
		return e, err
	}
	if !mvpNativeRestoreRequestContext(plan, l.marker.ScopeID, e.NativeRequest, c, e.SourceArchiveSHA256) || e.Writer.Owner.OperationID != plan.AttemptID || e.Writer.Image != "sha256:"+p.SourceRequest.ControlImageSHA256 || !reflect.DeepEqual(e.Writer.NativePostgreSQLRecovery.BackupReference, plan.Source.NativePostgreSQL) {
		return e, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_CHANGED")
	}
	return e, nil
}
func (l *locked) readMVPNativeRestoreOriginal(ctx context.Context, plan RestorePlan) (engine.NativePostgreSQLRestoreCompletion, error) {
	x, e := l.readMVPNativeRestoreExecution(ctx)
	if e != nil {
		return engine.NativePostgreSQLRestoreCompletion{}, e
	}
	r, id, e := l.nativeRestoreReceipt("sql", 2)
	if e != nil || r.Completion == nil || r.Stdout == nil {
		return engine.NativePostgreSQLRestoreCompletion{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_COMPLETION_REQUIRED")
	}
	return l.readMVPNativeRestoreOriginalReceipt(ctx, plan, x, r, id)
}

func (l *locked) readMVPNativeRestoreOriginalReceipt(ctx context.Context, plan RestorePlan, x mvpNativeRestoreExecution, r mvpNativeRestoreReceipt, id string) (engine.NativePostgreSQLRestoreCompletion, error) {
	if r.Stage != "sql" || r.Step != 2 || r.Completion == nil || r.Stdout == nil || r.Observation != nil || r.Execution != nil || r.PhysicalID != l.nativeRestorePhysicalID("sql", "native-writer") {
		return engine.NativePostgreSQLRestoreCompletion{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_COMPLETION_REQUIRED")
	}
	_, _, c, e := l.nativeRestoreInputs(ctx)
	if e != nil {
		return engine.NativePostgreSQLRestoreCompletion{}, e
	}
	complete, e := readMVPNativeRestoreCompletion(ctx, l.root, id, *r.Completion, plan, l.marker.ScopeID, x.NativeRequest, c, x.SourceArchiveSHA256)
	if e != nil {
		return complete, e
	}
	raw, e := regular(l.root, r.Stdout.Name, x.NativeRequest.Policy.MaximumManifestBytes)
	var stdout engine.NativePostgreSQLRestoreCompletion
	if e != nil || r.Stdout.Name != "mvp-native-restore-stdout-"+id+".json" || r.Stdout.Bytes != int64(len(raw)) || r.Stdout.SHA256 != wire.Digest(raw) || wire.StrictJSON(raw, &stdout) != nil {
		return complete, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STDOUT_CHANGED")
	}
	stdout.Original, stdout.OriginalSHA256 = raw, wire.Digest(raw)
	stdout, e = bindMVPNativeRestoreCompletion(plan, l.marker.ScopeID, x.NativeRequest, c, x.SourceArchiveSHA256, stdout)
	if e != nil {
		return complete, e
	}
	a, b := complete, stdout
	a.Original, a.OriginalSHA256, b.Original, b.OriginalSHA256 = nil, "", nil, ""
	if !reflect.DeepEqual(a, b) {
		return complete, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_STDOUT_CHANGED")
	}
	return complete, nil
}
