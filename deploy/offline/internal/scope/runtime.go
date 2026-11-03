package scope

import (
	"context"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
)

type runtimeEngine interface {
	imageEngine
	compose.RuntimeReader
	InspectVolume(context.Context, engine.Ownership) (engine.Volume, bool, error)
	VerifyVolumeConsumers(context.Context, engine.Ownership, []string) error
}
type runtimeOperator interface {
	CreateRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model) (compose.RuntimeStatus, error)
	ObserveRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model) (compose.RuntimeStatus, error)
}
type RuntimeResult struct {
	ScopeID       string                `json:"scope_id"`
	Resources     compose.RuntimeStatus `json:"resources"`
	Pending       bool                  `json:"pending"`
	Qualification string                `json:"qualification"`
}

func CreateRuntime(ctx context.Context, c configuration.Config, reconcile bool) (RuntimeResult, error) {
	worker, e := compose.New(c)
	if e != nil {
		return RuntimeResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RuntimeResult{}, e
	}
	defer client.Close()
	return createRuntime(ctx, c, client, worker, reconcile)
}
func createRuntime(ctx context.Context, c configuration.Config, client runtimeEngine, worker runtimeOperator, reconcile bool) (RuntimeResult, error) {
	result := RuntimeResult{Qualification: "isolated_resource_creation_only_not_application_bootstrap_health_or_target"}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	result.ScopeID = l.marker.ScopeID
	if processState(l.events).pending != "" {
		result.Pending = true
		return result, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	result.Pending = runtimeState(l.events).pending != ""
	verified, e := l.verifyRuntimeContext(ctx, client, worker, reconcile)
	result.Resources = verified.observed
	if e != nil {
		return result, e
	}
	p, model, scope, state, observed := verified.inputs, verified.model, verified.scope, verified.creation, verified.observed
	if state.completed {
		return result, nil
	}
	journal, e := l.root.Stat(journalName)
	required := int64(3 * maximumEventBytes)
	if reconcile {
		required = maximumEventBytes
	}
	if e != nil || journal.Size()+required > l.maximumJournalBytes() {
		return result, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	if reconcile {
		if state.pending == "" {
			return result, failure("OFFLINE_SCOPE_RUNTIME_ACTION_NOT_RECORDED")
		}
		record, observationError := runtimeObservation(p, observed)
		if observationError != nil || !runtimeProgress(*state.record, record) {
			record = *state.record
			observationError = failure("OFFLINE_SCOPE_RUNTIME_OUTCOME_UNRESOLVED")
		}
		complete := observationError == nil && observed.Status == "observed_complete_compose_resources" && len(record.Services) == len(model.Services)
		id, e := nonce()
		if e != nil {
			return result, e
		}
		event := Event{OperationID: id, Action: "runtime_reconcile", ReferenceOperationID: state.pending, Outcome: "unresolved", Runtime: &record, Code: "OFFLINE_SCOPE_RUNTIME_OUTCOME_UNRESOLVED"}
		if complete {
			event.Outcome = "completed"
			event.Code = ""
		}
		if _, e = l.checkEngine(ctx, client); e != nil {
			return result, e
		}
		if e = l.appendEvent(event); e != nil {
			return result, e
		}
		result.Pending = !complete
		if !complete {
			return result, failure("OFFLINE_SCOPE_RUNTIME_OUTCOME_UNRESOLVED")
		}
		return result, nil
	}
	if observed.Status != "observed_absent_compose_resources" {
		return result, failure("OFFLINE_SCOPE_UNRECORDED_RUNTIME_PRESERVED")
	}
	id, e := nonce()
	if e != nil {
		return result, e
	}
	initial := RuntimeRecord{DescriptorSHA256: p.record.DescriptorSHA256, ModelSHA256: p.record.ModelSHA256}
	if e = l.appendEvent(Event{OperationID: id, Action: "runtime_create", Outcome: "started", Runtime: &initial}); e != nil {
		return result, e
	}
	result.Pending = true
	created, actionError := worker.CreateRuntime(ctx, client, scope, model)
	result.Resources = created
	record, observationError := runtimeObservation(p, created)
	if observationError != nil {
		record = initial
		if actionError == nil {
			actionError = observationError
		}
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		actionError = e
	}
	complete := actionError == nil && created.Status == "observed_complete_compose_resources" && len(record.Services) == len(model.Services)
	event := Event{OperationID: id, Action: "runtime_create", Outcome: "uncertain", Runtime: &record, Code: "OFFLINE_SCOPE_RUNTIME_ACTION_UNCERTAIN"}
	if complete {
		event.Outcome = "completed"
		event.Code = ""
	}
	if e = l.appendEvent(event); e != nil {
		return result, e
	}
	result.Pending = !complete
	if !complete {
		return result, failure("OFFLINE_SCOPE_RUNTIME_ACTION_UNCERTAIN")
	}
	return result, nil
}
func runtimeObservation(p preparedInputs, s compose.RuntimeStatus) (RuntimeRecord, error) {
	r := RuntimeRecord{DescriptorSHA256: p.record.DescriptorSHA256, ModelSHA256: p.record.ModelSHA256, NetworkID: s.NetworkID, IngressNetworkID: s.IngressNetworkID, Services: s.Services}
	if !validRuntimeRecord(&r) {
		return r, failure("OFFLINE_SCOPE_RUNTIME_OBSERVATION_INVALID")
	}
	return r, nil
}
func runtimeIDsMatch(r RuntimeRecord, s compose.RuntimeStatus) bool {
	if r.NetworkID != s.NetworkID || r.IngressNetworkID != s.IngressNetworkID || len(r.Services) != len(s.Services) {
		return false
	}
	for key, value := range r.Services {
		if s.Services[key].ID != value.ID {
			return false
		}
	}
	return true
}

type verifiedRuntimeContext struct {
	inputs           preparedInputs
	model            compose.Model
	scope            compose.RenderContext
	creation         runtimeRecords
	observed         compose.RuntimeStatus
	previousTLSFiles []engine.ConfigurationFile
}

// verifyRuntimeContext performs no mutation and requires the caller's scope lock.
func (l *locked) verifyRuntimeContext(ctx context.Context, client runtimeEngine, worker runtimeOperator, allowPending bool) (verifiedRuntimeContext, error) {
	replacement := controlReplacementState(l.events)
	if replacement.record != nil && !replacement.complete {
		return verifiedRuntimeContext{}, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RECONCILIATION_REQUIRED")
	}
	return l.verifyRuntimeContextForReplacement(ctx, client, worker, allowPending, nil)
}
func (l *locked) verifyRuntimeContextForReplacement(ctx context.Context, client runtimeEngine, worker runtimeOperator, allowPending bool, replacement *ControlReplacementPlan) (verifiedRuntimeContext, error) {
	result := verifiedRuntimeContext{}
	c := l.config
	verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
	if e != nil {
		return result, e
	}
	if verified.ReleaseID != l.marker.ReleaseID || verified.Profile != l.marker.Profile {
		return result, failure("OFFLINE_SCOPE_RELEASE_CONTEXT_MISMATCH")
	}
	if e = validateImageAssets(l.events, verified.Manifest); e != nil {
		return result, e
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return result, failure("OFFLINE_SCOPE_COMPLETED_INPUT_SNAPSHOT_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return result, e
	}
	if e = validateProcessInputs(l.events, p, c); e != nil {
		return result, e
	}
	task := modelTaskState(l.events)
	if task.record != nil {
		actions := modelTaskActionState(l.events)
		if task.pending != "" || actions.pending != "" || !actions.removed || !actions.initialized {
			return result, failure("OFFLINE_SCOPE_COMPLETED_MODEL_INITIALIZER_REMOVAL_REQUIRED")
		}
	}
	state := runtimeState(l.events)
	result.creation = state
	if e = validateRuntimeInputs(state, p); e != nil {
		return result, e
	}
	if state.pending != "" && !allowPending {
		return result, failure("OFFLINE_SCOPE_RUNTIME_RECONCILIATION_REQUIRED")
	}
	volumes := volumeState(l.events)
	images := imageState(l.events)
	if len(images.pending) > 0 || len(volumes.pending) > 0 || len(volumes.roles) != len(p.descriptor.Roles) {
		return result, failure("OFFLINE_SCOPE_COMPLETED_VOLUME_IMAGE_PREPARATION_REQUIRED")
	}
	if e = validateVolumeInputs(volumes, p); e != nil {
		return result, e
	}
	for _, r := range volumes.roles {
		if r.Phase != phasePrepared {
			return result, failure("OFFLINE_SCOPE_COMPLETED_VOLUME_IMAGE_PREPARATION_REQUIRED")
		}
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return result, e
	}
	model, e := compose.DecodeModel(p.model, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	if replacement != nil {
		if previous := lastCompletedReplacement(generationPrefix(l.events, replacement.GenerationID, true)); previous != nil {
			model, e = compose.ControlConfigurationModel(model, previous.GenerationID)
			if e != nil {
				return result, e
			}
		}
		object, found, inspectError := client.InspectRuntime(ctx, l.marker.ScopeID, "control")
		if inspectError != nil {
			return result, inspectError
		}
		if !found || object.ID != replacement.Before.Services["control"].ID {
			_, model, e = l.replacementModels(ctx)
			if e != nil {
				return result, e
			}
			model, e = compose.ControlConfigurationModel(model, replacement.GenerationID)
			if e != nil {
				return result, e
			}
		}
	} else if _, complete := controlReplacementProof(l.events); complete {
		plan, readError := l.readControlReplacementPlan()
		if readError != nil {
			return result, readError
		}
		model, e = compose.ControlConfigurationModel(model, plan.GenerationID)
		if e != nil {
			return result, e
		}
		result.creation = effectiveRuntimeState(l.events)
	}
	if cumulativeUpgradeState(l.events).replaced {
		model, e = l.applyCumulativeRuntime(ctx, model, &p, images.completed)
		if e != nil {
			return result, e
		}
		result.creation = effectiveRuntimeState(l.events)
	}
	scope := compose.RenderContext{ScopeID: l.marker.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: map[string]bool{}}
	for _, record := range images.completed {
		scope.AllowedImages[record.ManifestDigest] = true
	}
	for _, service := range model.Services {
		foundExpected := false
		for _, record := range images.completed {
			if record.ManifestDigest != service.Image {
				continue
			}
			image, found, e := client.Inspect(ctx, service.Image)
			if e != nil {
				return result, e
			}
			if !found || !matchesImage(image, record) {
				return result, failure("OFFLINE_SCOPE_RECORDED_IMAGE_MISSING_OR_CHANGED")
			}
			foundExpected = true
			break
		}
		if !foundExpected {
			return result, failure("OFFLINE_SCOPE_MODEL_IMAGE_NOT_ADMITTED")
		}
	}
	observed, e := worker.ObserveRuntime(ctx, client, scope, model)
	if e != nil {
		return result, e
	}
	result.observed = observed
	if e = l.verifyCumulativeCurrentConfiguration(ctx, client, model, observed); e != nil {
		return result, e
	}
	ids := []string{}
	for _, s := range observed.Services {
		ids = append(ids, s.ID)
	}
	for _, role := range volumes.roles {
		owner := l.volumeOwner(role)
		if _, found, e := client.InspectVolume(ctx, owner); e != nil {
			return result, e
		} else if !found {
			return result, failure("OFFLINE_SCOPE_PREPARED_VOLUME_MISSING")
		}
		if e = client.VerifyVolumeConsumers(ctx, owner, ids); e != nil {
			return result, e
		}
	}

	checked := map[string]bool{}
	for _, event := range l.events {
		if event.ControlReplacement == nil || checked[event.ControlReplacement.GenerationID] {
			continue
		}
		id := event.ControlReplacement.GenerationID
		checked[id] = true
		projection := tlsProjectionState(generationPrefix(l.events, id, true))
		if projection.record == nil || projection.record.GenerationID != id {
			return result, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_CONFIGURATION_MISSING_OR_CHANGED")
		}
		generation, readError := l.readTLSProjectionPlan(*projection.record)
		if readError != nil {
			return result, readError
		}
		if _, found, inspectError := client.InspectVolume(ctx, generation.ConfigurationOwner); inspectError != nil || !found {
			return result, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_CONFIGURATION_MISSING_OR_CHANGED")
		}
		allowed := append([]string(nil), ids...)
		if inspector, ok := client.(restoredInputRuntimeView); ok && inspector.utilityID != "" {
			allowed = append(allowed, inspector.utilityID)
		}
		if inspectError := client.VerifyVolumeConsumers(ctx, generation.ConfigurationOwner, allowed); inspectError != nil {
			return result, inspectError
		}
	}
	result.inputs = p
	result.model = model
	result.scope = scope
	if replacement != nil {
		if e = l.validateReplacementObservation(*replacement, observed); e != nil {
			return result, e
		}
		return result, nil
	}
	if result.creation.completed && (observed.Status != "observed_complete_compose_resources" || !runtimeIDsMatch(*result.creation.record, observed)) {
		return result, failure("OFFLINE_SCOPE_RECORDED_RUNTIME_MISSING_OR_CHANGED")
	}
	return result, nil
}
