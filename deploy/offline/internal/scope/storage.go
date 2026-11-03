package scope

import (
	"context"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type storageEngine interface {
	imageEngine
	CreateVolume(context.Context, engine.Ownership) (engine.Volume, error)
	InspectVolume(context.Context, engine.Ownership) (engine.Volume, bool, error)
	CreateUtility(context.Context, engine.Ownership, string, int, int) (engine.Utility, error)
	InspectUtility(context.Context, engine.Ownership, string, int, int) (engine.Utility, bool, error)
	PutConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) error
	InspectConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
	RemoveUtility(context.Context, engine.Ownership, string, int, int) error
}
type StorageStatus struct {
	Status        string `json:"status"`
	ScopeID       string `json:"scope_id"`
	Profile       string `json:"profile"`
	RolesPrepared int    `json:"roles_prepared"`
	RolesPending  int    `json:"roles_pending"`
	Qualification string `json:"qualification"`
}

func (l *locked) storageStatus() StorageStatus {
	state := volumeState(l.events)
	count := 0
	for _, record := range state.roles {
		if record.Phase == phasePrepared {
			count++
		}
	}
	status := "observed_storage_preparation_progress"
	if len(state.pending) > 0 {
		status = "storage_action_uncertain"
	}
	return StorageStatus{Status: status, ScopeID: l.marker.ScopeID, Profile: l.marker.Profile, RolesPrepared: count, RolesPending: len(state.pending), Qualification: "private_payload_preparation_only_not_store_namespaces_database_application_or_target"}
}
func InitializeStorage(ctx context.Context, c configuration.Config, reconcile bool) (StorageStatus, error) {
	if c.Engine == nil || c.Engine.Utility == nil {
		return StorageStatus{}, failure("OFFLINE_SCOPE_UTILITY_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return StorageStatus{}, e
	}
	defer client.Close()
	return initializeStorage(ctx, c, client, reconcile)
}
func initializeStorage(ctx context.Context, c configuration.Config, client storageEngine, reconcile bool) (StorageStatus, error) {
	if ctx.Err() != nil {
		return StorageStatus{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return StorageStatus{}, e
	}
	defer l.close()
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return StorageStatus{}, e
	}
	if verified.Profile != l.marker.Profile || verified.ReleaseID != l.marker.ReleaseID {
		return StorageStatus{}, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return StorageStatus{}, e
	}
	record, outcome, _ := inputsState(l.events)
	if record == nil || outcome != "completed" {
		return StorageStatus{}, failure("OFFLINE_SCOPE_COMPLETED_INPUT_SNAPSHOT_REQUIRED")
	}
	p, e := l.readInputs(ctx, *record)
	if e != nil {
		return StorageStatus{}, e
	}
	state := volumeState(l.events)
	if processState(l.events).pending != "" {
		return StorageStatus{}, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	if runtimeState(l.events).pending != "" {
		return StorageStatus{}, failure("OFFLINE_SCOPE_RUNTIME_RECONCILIATION_REQUIRED")
	}
	if !reconcile && len(state.pending) > 0 {
		return StorageStatus{}, failure("OFFLINE_SCOPE_STORAGE_RECONCILIATION_REQUIRED")
	}
	if !reconcile {
		remaining := 0
		for _, role := range p.descriptor.Roles {
			if current, ok := state.roles[role.Key]; !ok || current.Phase != phasePrepared {
				remaining++
			}
		}
		journal, se := l.root.Stat(journalName)
		// Seven phase events plus one uncertain event and one explicit resolution per role.
		const eventsPerRoleWithRecovery = 9
		if se != nil || journal.Size()+int64(remaining*eventsPerRoleWithRecovery)*maximumEventBytes > l.maximumJournalBytes() {
			return StorageStatus{}, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
		}
	}
	if e = validateVolumeInputs(state, p); e != nil {
		return StorageStatus{}, e
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return StorageStatus{}, e
	}
	images := imageState(l.events)
	if len(images.pending) > 0 {
		return StorageStatus{}, failure("OFFLINE_SCOPE_IMAGE_RECONCILIATION_REQUIRED")
	}
	admitted, ok := images.completed[p.descriptor.ImageAssetID]
	if !ok {
		return StorageStatus{}, failure("OFFLINE_SCOPE_INITIALIZER_IMAGE_NOT_ADMITTED")
	}
	observed, found, e := client.Inspect(ctx, p.descriptor.ImageID)
	if e != nil {
		return StorageStatus{}, e
	}
	if !found || !matchesImage(observed, admitted) {
		return StorageStatus{}, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
	}
	if reconcile {
		for reference, event := range state.pending {
			r := *event.Volume
			resolved, e := l.observeVolume(ctx, client, r, p.files[r.Key])
			id, ne := nonce()
			if ne != nil {
				return StorageStatus{}, ne
			}
			resolution := Event{OperationID: id, Action: "volume_reconcile", ReferenceOperationID: reference, Volume: &r, Outcome: "unresolved", Code: "OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED"}
			if e == nil {
				resolution.Volume = &resolved
				resolution.Outcome = "completed"
				resolution.Code = ""
			}
			if _, engineError := l.checkEngine(ctx, client); engineError != nil {
				return StorageStatus{}, engineError
			}
			if je := l.appendEvent(resolution); je != nil {
				return StorageStatus{}, je
			}
			if e != nil {
				return l.storageStatus(), failure("OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED")
			}
		}
		return l.storageStatus(), nil
	}
	for _, role := range p.descriptor.Roles {
		if ctx.Err() != nil {
			return StorageStatus{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		current, exists := volumeState(l.events).roles[role.Key]
		if exists && current.Phase == phasePrepared {
			if _, found, e := client.InspectVolume(ctx, l.volumeOwner(current)); e != nil {
				return StorageStatus{}, e
			} else if !found {
				return StorageStatus{}, failure("OFFLINE_SCOPE_PREPARED_VOLUME_MISSING")
			}
			continue
		}
		if !exists {
			generation, e := nonce()
			if e != nil {
				return StorageStatus{}, e
			}
			current = VolumeRecord{Key: role.Key, DescriptorSHA256: p.record.DescriptorSHA256, PayloadSHA256: role.PayloadSHA256, ImageID: p.descriptor.ImageID, UID: role.UID, GID: role.GID, ResourceOperationID: generation, Phase: phaseReserved}
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return StorageStatus{}, e
		}
		if e = l.prepareVolume(ctx, client, current, p.files[role.Key]); e != nil {
			return l.storageStatus(), e
		}
	}
	if ctx.Err() != nil {
		return StorageStatus{}, failure("OFFLINE_SCOPE_COMMIT_UNCERTAIN")
	}
	return l.storageStatus(), nil
}
func validateVolumeInputs(state volumeRecords, p preparedInputs) error {
	roles := map[string]InputRole{}
	for _, role := range p.descriptor.Roles {
		roles[role.Key] = role
	}
	for key, record := range state.roles {
		role, ok := roles[key]
		if !ok || record.DescriptorSHA256 != p.record.DescriptorSHA256 || record.PayloadSHA256 != role.PayloadSHA256 || record.ImageID != p.descriptor.ImageID || record.UID != role.UID || record.GID != role.GID {
			return failure("OFFLINE_SCOPE_VOLUME_INPUT_IDENTITY_MISMATCH")
		}
	}
	return nil
}
func (l *locked) volumeOwner(r VolumeRecord) engine.Ownership {
	return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: r.ResourceOperationID, Role: r.Key}
}
func (l *locked) prepareVolume(ctx context.Context, client storageEngine, r VolumeRecord, files []engine.ConfigurationFile) error {
	if ctx.Err() != nil {
		return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	id, e := nonce()
	if e != nil {
		return e
	}
	initial := r
	event := Event{OperationID: id, Action: "volume_prepare", Outcome: "started", Volume: &initial}
	if e = l.appendEvent(event); e != nil {
		return e
	}
	checkpoint := func(phase string) error {
		r.Phase = phase
		copy := r
		return l.appendEvent(Event{OperationID: id, Action: "volume_prepare", Outcome: "checkpoint", Volume: &copy})
	}
	uncertain := func(cause error) error {
		copy := r
		if e := l.appendEvent(Event{OperationID: id, Action: "volume_prepare", Outcome: "uncertain", Code: safeEffectCode(cause), Volume: &copy}); e != nil {
			return e
		}
		return failure("OFFLINE_SCOPE_STORAGE_ACTION_UNCERTAIN")
	}
	owner := l.volumeOwner(r)
	if r.Phase == phaseReserved {
		if _, e = client.CreateVolume(ctx, owner); e != nil {
			return uncertain(e)
		}
		if e = checkpoint(phaseVolumeCreated); e != nil {
			return e
		}
	}
	if r.Phase == phaseVolumeCreated {
		utility, e := client.CreateUtility(ctx, owner, r.ImageID, r.UID, r.GID)
		if e != nil {
			return uncertain(e)
		}
		r.UtilityID = utility.ID
		if e = checkpoint(phaseUtilityCreated); e != nil {
			return e
		}
	}
	if r.Phase == phaseUtilityCreated {
		if e = checkpoint(phaseProjectionSubmitted); e != nil {
			return e
		}
		if e = l.putVolumePayload(ctx, client, r, files); e != nil {
			return uncertain(e)
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return uncertain(e)
		}
		if e = checkpoint(phasePayloadVerified); e != nil {
			return e
		}
	}
	if r.Phase == phasePayloadVerified {
		if e = checkpoint(phaseUtilityRemoveSubmitted); e != nil {
			return e
		}
		if e = client.RemoveUtility(ctx, owner, r.ImageID, r.UID, r.GID); e != nil {
			return uncertain(e)
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return uncertain(e)
		}
		r.Phase = phasePrepared
		copy := r
		if e = l.appendEvent(Event{OperationID: id, Action: "volume_prepare", Outcome: "completed", Volume: &copy}); e != nil {
			return e
		}
		return nil
	}
	return failure("OFFLINE_SCOPE_STORAGE_RECONCILIATION_REQUIRED")
}
func (l *locked) observeVolume(ctx context.Context, client storageEngine, r VolumeRecord, files []engine.ConfigurationFile) (VolumeRecord, error) {
	owner := l.volumeOwner(r)
	if _, found, e := client.InspectVolume(ctx, owner); e != nil {
		return r, e
	} else if !found {
		return r, failure("OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED")
	}
	utility, found, e := client.InspectUtility(ctx, owner, r.ImageID, r.UID, r.GID)
	if e != nil {
		return r, e
	}
	observed := r
	if !found {
		if r.Phase == phasePayloadVerified || r.Phase == phaseUtilityRemoveSubmitted {
			observed.Phase = phasePrepared
			return observed, nil
		}
		if r.Phase == phaseReserved || r.Phase == phaseVolumeCreated {
			observed.Phase = phaseVolumeCreated
			return observed, nil
		}
		return r, failure("OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED")
	}
	if r.UtilityID != "" && r.UtilityID != utility.ID {
		return r, failure("OFFLINE_SCOPE_STORAGE_UTILITY_ID_CHANGED")
	}
	observed.UtilityID = utility.ID
	state, e := l.inspectVolumePayload(ctx, client, r, files)
	if e != nil {
		return r, e
	}
	if state.Status == "verified_private_volume_payload" {
		observed.Phase = phasePayloadVerified
	} else if state.Status == "observed_empty_owned_volume" {
		observed.Phase = phaseUtilityCreated
	} else {
		return r, failure("OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED")
	}
	if phaseNumber(observed.Phase) < phaseNumber(r.Phase) {
		return r, failure("OFFLINE_SCOPE_STORAGE_OUTCOME_UNRESOLVED")
	}
	return observed, nil
}
