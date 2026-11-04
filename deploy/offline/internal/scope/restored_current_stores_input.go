package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoredCurrentStoresPlan struct {
	FormatVersion             int                         `json:"format_version"`
	ScopeID                   string                      `json:"scope_id"`
	GenerationID              string                      `json:"generation_id"`
	DatabaseProofSHA256       string                      `json:"database_proof_sha256"`
	DatabasePlanSHA256        string                      `json:"database_plan_sha256"`
	SelectedStoresProofSHA256 string                      `json:"selected_stores_proof_sha256"`
	SelectedStoresPlanSHA256  string                      `json:"selected_stores_plan_sha256"`
	Admission                 RestoredDependencyAdmission `json:"admission"`
	PayloadSHA256             string                      `json:"payload_sha256"`
	ContextSHA256             string                      `json:"context_sha256"`
	PostgresPID               int                         `json:"postgres_pid"`
	PostgresStartedAt         string                      `json:"postgres_started_at"`
	Initializer               string                      `json:"initializer"`
	Files                     []InputFile                 `json:"files"`
	Tasks                     []engine.DatabaseTask       `json:"tasks"`
}
type RestoredCurrentStoresInspection struct {
	Status        string                    `json:"status"`
	Plan          RestoredCurrentStoresPlan `json:"plan"`
	PlanSHA256    string                    `json:"plan_sha256"`
	Qualification string                    `json:"qualification"`
}

func InspectRestoredCurrentStores(ctx context.Context, c configuration.Config) (RestoredCurrentStoresInspection, error) {
	var out RestoredCurrentStoresInspection
	if c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil {
		return out, failure("OFFLINE_SCOPE_CURRENT_STORES_CONFIGURATION_REQUIRED")
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
	p, _, e := l.makeRestoredCurrentStoresPlan(ctx, client, worker, generation)
	if e != nil {
		return out, e
	}
	return RestoredCurrentStoresInspection{Status: "validated_current_store_check_plan_no_effects", Plan: p, PlanSHA256: wire.Digest(encode(p)), Qualification: "readonly_admission_only_not_current_store_readback_or_activation"}, nil
}
func (l *locked) makeRestoredCurrentStoresPlan(ctx context.Context, c controlReplacementEngine, w runtimeOperator, generation string) (RestoredCurrentStoresPlan, []engine.ConfigurationFile, error) {
	var out RestoredCurrentStoresPlan
	proof, ok := liveDatabaseProof(l.events)
	s := liveDatabaseState(l.events)
	if !ok || s.record == nil {
		return out, nil, failure("OFFLINE_SCOPE_CURRENT_LIVE_DATABASE_QUALIFICATION_REQUIRED")
	}
	prior, e := l.readLiveDatabasePlan(*s.record)
	if e != nil {
		return out, nil, e
	}
	current, files, e := l.makeRestoredLiveDatabasePlan(ctx, c, w, generation)
	if e != nil {
		return out, nil, e
	}
	// Compare every process/configuration fact, normalizing only the newly
	// proposed generation/task input identities. Old completed SQL is not readiness.
	expected := prior
	expected.GenerationID = current.GenerationID
	expected.Tasks = current.Tasks
	if !reflect.DeepEqual(expected, current) {
		return out, nil, failure("OFFLINE_SCOPE_CURRENT_LIVE_DATABASE_GENERATION_CHANGED")
	}
	if e = currentSelectedStoreProfile(files, l.marker.Profile); e != nil {
		return out, nil, e
	}
	storesProof, ok := restoreStoresProof(l.events)
	if !ok {
		return out, nil, failure("OFFLINE_SCOPE_CURRENT_SELECTED_STORES_PROOF_REQUIRED")
	}
	stores, e := l.readRestoreStoresPlan()
	if e != nil {
		return out, nil, e
	}
	verified, e := l.verifyRuntimeContext(ctx, c, w, false)
	if e != nil {
		return out, nil, e
	}
	tasks, e := assembleCurrentStoreTasks(l, stores, current, verified.model)
	if e != nil {
		return out, nil, e
	}
	out = RestoredCurrentStoresPlan{FormatVersion: 1, ScopeID: l.marker.ScopeID, GenerationID: generation, DatabaseProofSHA256: proof, DatabasePlanSHA256: s.record.PlanSHA256, SelectedStoresProofSHA256: storesProof, SelectedStoresPlanSHA256: wire.Digest(encode(stores)), Admission: liveDatabasePlanRecord(current).Admission, PayloadSHA256: current.PayloadSHA256, ContextSHA256: current.ContextSHA256, PostgresPID: current.PostgresPID, PostgresStartedAt: current.PostgresStartedAt, Initializer: current.Initializer, Files: current.Files, Tasks: tasks}
	if _, e = l.checkEngine(ctx, c); e != nil {
		return RestoredCurrentStoresPlan{}, nil, e
	}
	return out, files, nil
}

// Training and preparation select their active runtime through artifact_root,
// as defined by the control configuration contract, rather than a boolean.
func currentSelectedStoreProfile(files []engine.ConfigurationFile, profiles ...string) error {
	raw, e := restoreNamedFile(files, "runtime.yaml")
	type storeRoot struct {
		ArtifactRoot string `json:"artifact_root"`
	}
	var envelope struct {
		Server struct {
			Profile string `json:"application_profile"`
		} `json:"server"`
		Models      storeRoot `json:"models"`
		Datasets    storeRoot `json:"datasets"`
		Training    storeRoot `json:"training"`
		Preparation storeRoot `json:"model_preparation"`
	}
	if e != nil || wire.UniqueJSON(raw, &envelope) != nil || len(profiles) > 1 {
		return failure("OFFLINE_SCOPE_CURRENT_STORE_ACTIVE_PROFILE_UNSUPPORTED")
	}
	if len(profiles) == 1 && wire.IsCumulativeCPUProfile(profiles[0]) {
		application := "releases"
		if profiles[0] == wire.CumulativePredecessorCPUProfile {
			application = "localization"
		}
		if profiles[0] == wire.CumulativeCacheCPUProfile {
			application = wire.CumulativeCacheApplicationProfile
		}
		if profiles[0] == wire.MVPPostgreSQLCPUProfile {
			application = ""
		}
		if envelope.Server.Profile != application || envelope.Models.ArtifactRoot != "/var/lib/projectone/models/private" || envelope.Datasets.ArtifactRoot != "/var/lib/projectone/datasets/private" || envelope.Training.ArtifactRoot != "/var/lib/projectone/training/private" || envelope.Preparation.ArtifactRoot != "/var/lib/projectone/preparations/private" {
			return failure("OFFLINE_SCOPE_CURRENT_STORE_ACTIVE_PROFILE_UNSUPPORTED")
		}
	} else if envelope.Training.ArtifactRoot != "" || envelope.Preparation.ArtifactRoot != "" {
		return failure("OFFLINE_SCOPE_CURRENT_STORE_ACTIVE_PROFILE_UNSUPPORTED")
	}
	return nil
}
func assembleCurrentStoreTasks(l *locked, stores RestoreStorePlan, current RestoredLiveDatabasePlan, model compose.Model) ([]engine.DatabaseTask, error) {
	bad := func() ([]engine.DatabaseTask, error) {
		return nil, failure("OFFLINE_SCOPE_CURRENT_STORE_PROFILE_OR_MOUNT_UNSUPPORTED")
	}
	kinds := restoredStoreCatalog()
	if !wire.IsCumulativeCPUProfile(l.marker.Profile) {
		kinds = kinds[:2]
	}
	native := stores.Native != nil
	if native != (l.marker.Profile == wire.MVPPostgreSQLCPUProfile) {
		return bad()
	}
	if native {
		restore := restoreState(l.events).record
		if restore == nil || stores.FormatVersion != 2 || stores.GenerationID != restore.AttemptID || !validMVPNativeRestoreStageRecord(stores.Native, mvpNativeRestoreStoresStage, restore.AttemptID) {
			return bad()
		}
	}
	// Native activation may perform the first runtime-role preparation before
	// the same two readonly live checks. Only their shared input/network facts
	// are copied here; no preparation task is executed by current-store checks.
	validTaskCount := len(current.Tasks) == 2
	if native {
		validTaskCount = current.FormatVersion == 2 && len(current.Tasks) == 2 && current.Tasks[0].Kind == engine.DatabaseSchemaCheck && current.Tasks[1].Kind == engine.DatabaseRuntimeCheck
	}
	if native && current.FormatVersion == 2 && len(current.Tasks) == 3 {
		validTaskCount = current.Tasks[0].Kind == engine.DatabaseRuntimeRestorePrepare && current.Tasks[1].Kind == engine.DatabaseSchemaCheck && current.Tasks[2].Kind == engine.DatabaseRuntimeCheck
	}
	if !validTaskCount || len(stores.Jobs) != len(kinds) {
		return bad()
	}
	tasks := []engine.DatabaseTask{}
	for i, item := range kinds {
		job := stores.Jobs[i]
		if job.Role != item.role || job.Verify.Kind != item.verifyKind || job.Verify.OutputOwner == nil || native && !reflect.DeepEqual(job.Import, engine.DatabaseTask{}) {
			return bad()
		}
		target, ok := volumeState(l.events).roles[item.role]
		if !ok || target.Phase != phasePrepared || *job.Verify.OutputOwner != l.volumeOwner(target) || target.UID != job.Verify.UID || target.GID != job.Verify.GID {
			return bad()
		}
		task := job.Verify
		task.Owner.OperationID = current.GenerationID
		task.VolumeOwner = current.Tasks[0].VolumeOwner
		task.NetworkID = current.Tasks[0].NetworkID
		task.Peers = map[string]string{}
		for role, id := range current.Tasks[0].Peers {
			task.Peers[role] = id
		}
		if task.Image != current.Tasks[0].Image || task.UID != current.Tasks[0].UID || task.GID != current.Tasks[0].GID || !reflect.DeepEqual(task.LocalizationOwner, current.Tasks[0].LocalizationOwner) || task.SourceOwner != nil || task.StoreRestore != nil || (!item.verifyReason && task.Reason != "") || (item.verifyReason && !engine.ValidStoreTaskReason(task.Reason)) {
			return bad()
		}
		destination, e := task.RestoredStoreDestination()
		if e != nil {
			return bad()
		}
		found := 0
		for _, mount := range model.Services["control"].Mounts {
			if mount.Source == item.role {
				if mount.Type != "volume" || mount.Target != destination || mount.ReadOnly {
					return bad()
				}
				found++
			}
		}
		if found != 1 {
			return bad()
		}
		tasks = append(tasks, task)
	}
	for _, role := range []string{"training", "preparations"} {
		if _, exists := volumeState(l.events).roles[role]; exists && len(kinds) == 2 {
			return bad()
		}
	}
	return tasks, nil
}
