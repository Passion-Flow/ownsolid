package scope

import (
	"context"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Only hashes, fixed task identities and process facts are public. Credentials
// remain in protected inputs or the separately returned in-memory payload.
type RestoredLiveDatabasePlan struct {
	FormatVersion          int                         `json:"format_version"`
	ScopeID                string                      `json:"scope_id"`
	GenerationID           string                      `json:"generation_id"`
	ReplacementProofSHA256 string                      `json:"replacement_proof_sha256"`
	TLSProofSHA256         string                      `json:"tls_proof_sha256"`
	TLSPlanSHA256          string                      `json:"tls_plan_sha256"`
	DatabaseProofSHA256    string                      `json:"database_proof_sha256"`
	EffectiveRuntimeSHA256 string                      `json:"effective_runtime_sha256"`
	PayloadSHA256          string                      `json:"payload_sha256"`
	ContextSHA256          string                      `json:"context_sha256"`
	PostgresStartedAt      string                      `json:"postgres_started_at"`
	PostgresPID            int                         `json:"postgres_pid"`
	Initializer            string                      `json:"initializer"`
	Files                  []InputFile                 `json:"files"`
	Tasks                  []engine.DatabaseTask       `json:"tasks"`
	NativeRuntime          *mvpNativeRuntimeActivation `json:"native_runtime,omitempty"`
}

type RestoredLiveDatabaseInspection struct {
	Status        string                   `json:"status"`
	Plan          RestoredLiveDatabasePlan `json:"plan"`
	PlanSHA256    string                   `json:"plan_sha256"`
	Qualification string                   `json:"qualification"`
}

func InspectRestoredLiveDatabase(ctx context.Context, c configuration.Config) (RestoredLiveDatabaseInspection, error) {
	var out RestoredLiveDatabaseInspection
	if c.Engine == nil || c.Compose == nil {
		return out, failure("OFFLINE_SCOPE_LIVE_DATABASE_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return out, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return out, e
	}
	l, e := open(c, false)
	if e != nil {
		return out, e
	}
	defer l.close()
	generation, e := nonce()
	if e != nil {
		return out, e
	}
	plan, _, e := l.makeRestoredLiveDatabasePlan(ctx, client, worker, generation)
	if e != nil {
		return out, e
	}
	return RestoredLiveDatabaseInspection{Status: "validated_live_database_check_plan_no_effects", Plan: plan, PlanSHA256: wire.Digest(encode(plan)), Qualification: "readonly_current_admission_only_not_SQL_schema_runtime_authority_or_activation"}, nil
}

func (l *locked) makeRestoredLiveDatabasePlan(ctx context.Context, client controlReplacementEngine, worker runtimeOperator, generation string) (RestoredLiveDatabasePlan, []engine.ConfigurationFile, error) {
	var out RestoredLiveDatabasePlan
	if !identityPattern.MatchString(generation) {
		return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_GENERATION_REQUIRED")
	}
	for _, event := range l.events {
		if event.OperationID == generation || event.RestoredCurrentStores != nil && event.RestoredCurrentStores.GenerationID == generation || event.RestoredLiveDatabase != nil && event.RestoredLiveDatabase.GenerationID == generation || event.Volume != nil && event.Volume.ResourceOperationID == generation || event.TLSProjection != nil && event.TLSProjection.GenerationID == generation || event.ControlReplacement != nil && event.ControlReplacement.GenerationID == generation {
			return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_UNUSED_GENERATION_REQUIRED")
		}
	}
	admission, e := l.admitRestoredDependency(ctx, client, worker)
	if e != nil {
		return out, nil, e
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		return out, nil, e
	}
	postgres, found, e := client.InspectRuntime(ctx, l.marker.ScopeID, "postgres")
	started, parse := time.Parse(time.RFC3339Nano, postgres.State.StartedAt)
	if e != nil || !found || postgres.ID != verified.observed.Services["postgres"].ID || postgres.State.Status != "running" || !postgres.State.Running || postgres.State.PID <= 0 || postgres.State.ExitCode != 0 || postgres.State.Paused || postgres.State.Dead || postgres.State.OOMKilled || postgres.State.Restarting || parse != nil || started.IsZero() {
		return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_RUNNING_DEPENDENCY_REQUIRED")
	}
	tls, current, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		return out, nil, e
	}
	restore, e := l.readBoundRestorePlan()
	if e != nil {
		return out, nil, e
	}
	base, original, e := l.makeRestoreDatabaseRecord(ctx, restore)
	if e != nil {
		return out, nil, e
	}
	raw, e := restoreNamedFile(original, restoredRuntimeContextName)
	var restored wire.RestoredRuntimeContext
	if e != nil || wire.StrictJSON(raw, &restored) != nil || wire.Digest(raw) != base.ContextSHA256 {
		return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_RESTORE_CONTEXT_CHANGED")
	}
	_, previousOwner, previousPayload, e := l.previousTLSConfiguration(ctx, tls.GenerationID)
	if e != nil || previousOwner != tls.PreviousOwner {
		return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_TLS_PREDECESSOR_CHANGED")
	}
	files, e := mergeCurrentRestoredLiveInput(verified.inputs, tls, current, previousPayload, restored, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return out, nil, e
	}
	databaseProof, ok := restoreDatabaseProof(l.events)
	if !ok {
		return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_RESTORE_PROOF_REQUIRED")
	}
	native, e := l.makeMVPNativeActivation(restore, restored, files, base.ContextSHA256)
	if e != nil {
		return out, nil, e
	}
	owner := engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: generation, Role: engine.StoreRestoreConfigRole + "-" + generation}
	tasks := []engine.DatabaseTask{}
	for _, kind := range liveDatabaseTaskKinds(native) {
		task := base.task(kind)
		task.Owner.OperationID, task.VolumeOwner = generation, owner
		task.NetworkID = verified.creation.record.NetworkID
		task.Peers = map[string]string{}
		for role, service := range verified.creation.record.Services {
			task.Peers[role] = service.ID
		}
		if task.ValidateRestoredDatabaseTask() != nil || task.OutputOwner != nil || task.SourceOwner != nil || task.StoreRestore != nil || task.Reason != "" {
			return out, nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_CHECK_TASK_INVALID")
		}
		tasks = append(tasks, task)
	}
	out = RestoredLiveDatabasePlan{FormatVersion: 1, ScopeID: l.marker.ScopeID, GenerationID: generation, ReplacementProofSHA256: admission.ReplacementProofSHA256, TLSProofSHA256: admission.TLSProofSHA256, TLSPlanSHA256: admission.ConfigurationPlanSHA256, DatabaseProofSHA256: databaseProof, EffectiveRuntimeSHA256: admission.EffectiveRuntimeSHA256, PayloadSHA256: restoreConfigurationDigest(files), ContextSHA256: base.ContextSHA256, PostgresStartedAt: postgres.State.StartedAt, PostgresPID: postgres.State.PID, Initializer: base.Initializer, Files: []InputFile{}, Tasks: tasks, NativeRuntime: native}
	if native != nil {
		out.FormatVersion = 2
	}
	for _, file := range files {
		out.Files = append(out.Files, InputFile{Name: file.Name, Bytes: int64(len(file.Contents)), SHA256: wire.Digest(file.Contents)})
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return RestoredLiveDatabasePlan{}, nil, e
	}
	return out, files, nil
}

func mergeCurrentRestoredLiveInput(p preparedInputs, tls RestoredTLSPlan, current []engine.ConfigurationFile, previousPayload string, restored wire.RestoredRuntimeContext, maximum int64) ([]engine.ConfigurationFile, error) {
	model, e := compose.DecodeModel(p.model, maximum)
	if e != nil {
		return nil, e
	}
	if tls.PayloadSHA256 != restoreConfigurationDigest(current) || !wire.ValidSHA256(previousPayload) || tls.PreviousPayloadSHA256 != previousPayload {
		return nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_TLS_PAYLOAD_CHANGED")
	}
	replacements := map[string][]byte{}
	for _, file := range current {
		if tls.FormatVersion == 2 || file.Name == tls.CertificateName || file.Name == tls.PrivateKeyName {
			replacements[file.Name] = append([]byte{}, file.Contents...)
		}
	}
	if tls.FormatVersion == 1 && len(replacements) != 2 || tls.FormatVersion == 2 && (len(replacements) != len(tls.ConfigurationFiles) || replacements[tls.CertificateName] == nil || replacements[tls.PrivateKeyName] == nil) {
		return nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_CURRENT_TLS_FILES_REQUIRED")
	}
	copy := p
	copy.files = map[string][]engine.ConfigurationFile{}
	for role, files := range p.files {
		copy.files[role] = append([]engine.ConfigurationFile{}, files...)
		for i, file := range copy.files[role] {
			copy.files[role][i].Contents = append([]byte{}, file.Contents...)
			if role == "operator-config" && replacements[file.Name] != nil {
				copy.files[role][i].Contents = append([]byte{}, replacements[file.Name]...)
			}
		}
	}
	copy.files["control-config"] = append([]engine.ConfigurationFile{}, current...)
	files, e := mergeRestoreQualificationInput(copy, model, restored)
	if e != nil {
		return nil, e
	}
	for _, file := range files {
		if expected, ok := replacements[file.Name]; ok && !reflect.DeepEqual(expected, file.Contents) {
			return nil, failure("OFFLINE_SCOPE_LIVE_DATABASE_EFFECTIVE_TLS_MERGE_FAILED")
		}
	}
	return files, nil
}
