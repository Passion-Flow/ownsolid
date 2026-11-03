package scope

import (
	"context"
	"math"
	"reflect"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type processEngine interface {
	runtimeEngine
	AdmitRuntimeResources(context.Context, engine.ResourceRequirement, int) (engine.ResourceAdmission, error)
}
type processOperator interface {
	runtimeOperator
	StartRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error)
	StopRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error)
}
type databaseProcessOperator interface {
	StartDatabaseRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error)
}
type ProcessResult struct {
	ScopeID       string                `json:"scope_id"`
	Status        string                `json:"status"`
	Pending       bool                  `json:"pending"`
	Resources     compose.RuntimeStatus `json:"resources"`
	Qualification string                `json:"qualification"`
}

func ProcessRuntime(ctx context.Context, c configuration.Config, target string, reconcile bool) (ProcessResult, error) {
	worker, e := compose.New(c)
	if e != nil {
		return ProcessResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ProcessResult{}, e
	}
	defer client.Close()
	return processRuntime(ctx, c, client, worker, target, reconcile)
}
func StartDatabase(ctx context.Context, c configuration.Config) (ProcessResult, error) {
	worker, e := compose.New(c)
	if e != nil {
		return ProcessResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ProcessResult{}, e
	}
	defer client.Close()
	return processRuntimeSelected(ctx, c, client, worker, "running", false, []string{"postgres"})
}
func processRuntime(ctx context.Context, c configuration.Config, client processEngine, worker processOperator, target string, reconcile bool) (ProcessResult, error) {
	return processRuntimeSelected(ctx, c, client, worker, target, reconcile, nil)
}
func processRuntimeSelected(ctx context.Context, c configuration.Config, client processEngine, worker processOperator, target string, reconcile bool, selected []string) (ProcessResult, error) {
	result := ProcessResult{Qualification: "observed_process_action_only_not_application_bootstrap_readiness_or_target"}
	if c.Compose == nil || c.Compose.Process == nil {
		return result, failure("OFFLINE_SCOPE_PROCESS_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return result, e
	}
	if !reconcile && target != "running" && target != "stopped" {
		return result, failure("OFFLINE_SCOPE_PROCESS_TARGET_INVALID")
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}

	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	return l.processSelectedLocked(ctx, client, worker, target, reconcile, selected)
}
func (l *locked) processLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool) (ProcessResult, error) {
	return l.processSelectedLocked(ctx, client, worker, target, reconcile, nil)
}
func (l *locked) processSelectedLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool, selected []string) (ProcessResult, error) {
	return l.processSelectedAdmittedLocked(ctx, client, worker, target, reconcile, selected, false)
}

func (l *locked) processSelectedAdmittedLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool, selected []string, restoredDependency bool) (ProcessResult, error) {
	mode := ordinaryProcess
	if restoredDependency {
		mode = restoredDependencyProcess
	}
	return l.processSelectedWithAdmissionLocked(ctx, client, worker, target, reconcile, selected, mode)
}

type restoredProcessMode int

const (
	ordinaryProcess restoredProcessMode = iota
	restoredDependencyProcess
	restoredApplicationProcess
)

func (l *locked) processSelectedWithAdmissionLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool, selected []string, mode restoredProcessMode) (ProcessResult, error) {
	return l.processSelectedAttemptLocked(ctx, client, worker, target, reconcile, selected, mode, false)
}

func (l *locked) processSelectedAttemptLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool, selected []string, mode restoredProcessMode, retry bool) (ProcessResult, error) {
	return l.processSelectedRecoveryAttemptLocked(ctx, client, worker, target, reconcile, selected, mode, retry, nil)
}

func (l *locked) processSelectedRecoveryAttemptLocked(ctx context.Context, client processEngine, worker processOperator, target string, reconcile bool, selected []string, mode restoredProcessMode, retry bool, recovery *configuration.ProcessRecoveryRequest) (ProcessResult, error) {
	result := ProcessResult{Qualification: "observed_process_action_only_not_application_bootstrap_readiness_or_target"}
	c := l.config
	if recovery != nil && (recovery.Validate() != nil || reconcile || target != "running" || len(selected) != 1 || mode == ordinaryProcess || mode == restoredDependencyProcess && selected[0] != "postgres" || mode == restoredApplicationProcess && (selected[0] != "control" || !retry)) {
		return result, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_SELECTION_INVALID")
	}
	if c.Compose == nil || c.Compose.Process == nil {
		return result, failure("OFFLINE_SCOPE_PROCESS_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return result, e
	}
	if !reconcile && target != "running" && target != "stopped" {
		return result, failure("OFFLINE_SCOPE_PROCESS_TARGET_INVALID")
	}
	if ctx.Err() != nil {
		return result, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	result.ScopeID = l.marker.ScopeID
	retryOf := ""
	lateFailureRetry := false
	if retry {
		if processState(l.events).pending != "" {
			result.Pending = true
			return result, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
		}
		creation := effectiveRuntimeState(l.events)
		if reconcile || target != "running" || mode == ordinaryProcess || len(selected) != 1 || creation.record == nil {
			return result, failure("OFFLINE_SCOPE_PROCESS_RETRY_SELECTION_INVALID")
		}
		retryOf = retryableStartID(l.events, wire.Digest(encode(*creation.record)), selected)
		if retryOf == "" && mode == restoredApplicationProcess && recovery == nil {
			var inspectError error
			retryOf, inspectError = l.observedLateControlRetry(ctx, client, wire.Digest(encode(*creation.record)))
			if inspectError != nil {
				return result, inspectError
			}
			lateFailureRetry = retryOf != ""
		}
		if retryOf == "" {
			return result, failure("OFFLINE_SCOPE_PROCESS_NO_RETRYABLE_START")
		}
		if recovery != nil && selected[0] == "control" && retryOf != recovery.FailedControlStartOperationID {
			return result, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_PREDECESSOR_CHANGED")
		}
	}
	state := processState(l.events)
	if reconcile && state.record != nil {
		selected = state.record.SelectedServices
	}
	if len(selected) > 0 {
		if len(selected) != 1 || selected[0] != "postgres" && selected[0] != "control" || !reconcile && target != "running" {
			return result, failure("OFFLINE_SCOPE_PROCESS_SELECTION_INVALID")
		}
		if !reconcile {
			if selected[0] == "postgres" {
				if _, ok := worker.(databaseProcessOperator); !ok {
					return result, failure("OFFLINE_SCOPE_DATABASE_PROCESS_OPERATOR_REQUIRED")
				}
			} else {
				if mode != restoredApplicationProcess {
					return result, failure("OFFLINE_SCOPE_RESTORED_ACTIVATION_REQUIRED")
				}
				if _, ok := worker.(controlProcessOperator); !ok {
					return result, failure("OFFLINE_SCOPE_CONTROL_PROCESS_OPERATOR_REQUIRED")
				}
			}
		}
	}
	if !reconcile && target == "running" && mode != restoredApplicationProcess {
		if e := restoreMutationGuard(l.events, Event{Action: "process_start", Process: &ProcessRecord{SelectedServices: selected}}); e != nil {
			return result, e
		}
	}
	replacement := controlReplacementState(l.events)
	var dependency *RestoredDependencyAdmission
	var application *RestoredApplicationAdmission
	if mode != ordinaryProcess && (!replacement.complete || reconcile || target != "running" || len(selected) != 1 || mode == restoredDependencyProcess && selected[0] != "postgres" || mode == restoredApplicationProcess && selected[0] != "control") {
		return result, failure("OFFLINE_SCOPE_COMPLETED_CONTROL_REPLACEMENT_REQUIRED")
	}
	if replacement.record != nil {
		if !reconcile && target == "running" {
			if mode == ordinaryProcess {
				return result, failure("OFFLINE_SCOPE_RESTORED_ACTIVATION_REQUIRED")
			}
			sdk, ok := client.(controlReplacementEngine)
			if !ok {
				return result, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_READBACK_REQUIRED")
			}
			var e error
			if mode == restoredApplicationProcess {
				current, ok := client.(restoredApplicationEngine)
				if !ok {
					return result, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_READBACK_REQUIRED")
				}
				application, e = l.admitRestoredApplicationRecovery(ctx, current, worker, retryOf, recovery)
			} else {
				dependency, e = l.admitRestoredDependencyRecovery(ctx, sdk, worker, retryOf, recovery)
			}
			if e != nil {
				return result, e
			}
		}
		if !replacement.complete {
			if state.pending != "" || reconcile || target != "stopped" {
				return result, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_RECONCILIATION_REQUIRED")
			}
			sdk, ok := client.(tlsProjectionEngine)
			if !ok {
				return result, failure("OFFLINE_SCOPE_CONTROL_REPLACEMENT_GENERATED_INSPECTION_REQUIRED")
			}
			plan, readError := l.readControlReplacementPlan()
			if readError != nil {
				return result, readError
			}
			view, readError := l.controlReplacementRuntimeView(ctx, sdk)
			if readError != nil {
				return result, readError
			}
			observed, readError := l.verifyRuntimeContextForReplacement(ctx, view, worker, false, &plan)
			result.Resources = observed.observed
			result.Pending = true
			if readError != nil {
				return result, readError
			}
			result.Status = "observed_replacement_peers_stopped_no_process_effect"
			return result, nil
		}
	}
	result.Pending = state.pending != ""
	if state.pending != "" && !reconcile {
		return result, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	var inspectionError error
	client, inspectionError = l.tlsProjectionProcessClient(ctx, client)
	if inspectionError != nil {
		return result, inspectionError
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		return result, e
	}
	if !verified.creation.completed || verified.creation.record == nil {
		return result, failure("OFFLINE_SCOPE_COMPLETED_RUNTIME_CREATION_REQUIRED")
	}
	if !retry && !reconcile && target == "running" && retryableStartID(l.events, wire.Digest(encode(*verified.creation.record)), selected) != "" {
		return result, failure("OFFLINE_SCOPE_PROCESS_EXPLICIT_RETRY_REQUIRED")
	}
	if len(selected) > 0 {
		if _, ok := verified.model.Services[selected[0]]; !ok {
			if selected[0] == "postgres" {
				return result, failure("OFFLINE_SCOPE_POSTGRES_SERVICE_REQUIRED")
			}
			return result, failure("OFFLINE_SCOPE_SELECTED_SERVICE_REQUIRED")
		}
	}
	if e = validateProcessInputs(l.events, verified.inputs, c); e != nil {
		return result, e
	}
	observed, before, e := captureProcess(ctx, client, worker, verified)
	result.Resources = observed
	if e != nil {
		return result, e
	}
	if reconcile {
		if state.pending == "" {
			result.Status = "observed_no_pending_process_action"
			return result, nil
		}
		if e = admitProcessJournal(l, 1); e != nil {
			return result, e
		}
		record := *state.record
		record.After = before
		outcome, code := processOutcome(record, verified.model)
		if _, e = l.checkEngine(ctx, client); e != nil {
			return result, e
		}
		id, e := nonce()
		if e != nil {
			return result, e
		}
		if e = l.appendEvent(Event{OperationID: id, Action: "process_reconcile", ReferenceOperationID: state.pending, Outcome: outcome, Code: code, Process: &record}); e != nil {
			return result, e
		}
		result.Pending = outcome == "unresolved"
		result.Status = "recorded_process_" + outcome
		if outcome != "completed" {
			return result, failure(code)
		}
		return result, nil
	}
	if selectedProcessTarget(before, target, selected) {
		result.Status = "observed_already_" + target
		return result, nil
	}
	if e = admitProcessJournal(l, 3); e != nil {
		return result, e
	}
	initial := ProcessRecord{RetryOfOperationID: retryOf, AttemptVersion: processAttemptVersion, CreationSHA256: wire.Digest(encode(*verified.creation.record)), Target: target, Before: before, SelectedServices: selected, RestoredDependency: dependency, RestoredApplication: application}
	if lateFailureRetry {
		initial.AttemptVersion = processLateFailureAttemptVersion
	}
	if recovery != nil {
		context, valid := stoppedRecoveryAtSnapshot(l.events, initial.CreationSHA256, before, recovery.FailedControlStartOperationID, mode == restoredApplicationProcess)
		if !valid {
			return result, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_CONTEXT_CHANGED")
		}
		initial.AttemptVersion, initial.RecoveryOfOperationID, initial.RecoveryStopOperationID, initial.RecoveryReason = processRecoveryAttemptVersion, recovery.FailedControlStartOperationID, context.stopID, recovery.Reason
	}
	if retry && !validProcessRetry(l.events, initial) {
		return result, failure("OFFLINE_SCOPE_PROCESS_RETRY_PREDECESSOR_CHANGED")
	}
	if target == "running" {
		if len(selected) == 0 || selected[0] == "control" {
			if e = l.admitEffectiveTLSStart(ctx); e != nil {
				return result, e
			}
		}
		if e = restoreMutationGuard(l.events, Event{Action: "process_start", Process: &initial}); e != nil {
			return result, e
		}
		need, e := additionalSelectedProcessResources(verified.model, before, selected)
		if e != nil {
			return result, e
		}
		admitted, e := client.AdmitRuntimeResources(ctx, need, c.Compose.Process.ReservePercent)
		if e != nil {
			return result, e
		}
		if admitted.ValidateRecorded() != nil || admitted.Requirement != need || admitted.ReservePercent != c.Compose.Process.ReservePercent {
			return result, failure("OFFLINE_SCOPE_PROCESS_ADMISSION_CONTEXT_INVALID")
		}
		initial.Admission = &admitted
	}
	id, e := nonce()
	if e != nil {
		return result, e
	}
	action := "process_stop"
	if target == "running" {
		action = "process_start"
	}
	if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", Process: &initial}); e != nil {
		return result, e
	}
	result.Pending = true
	var actionError error
	if len(selected) > 0 {
		if selected[0] == "control" {
			_, actionError = worker.(controlProcessOperator).StartControlRuntime(ctx, client, verified.scope, verified.model, verified.observed)
		} else {
			_, actionError = worker.(databaseProcessOperator).StartDatabaseRuntime(ctx, client, verified.scope, verified.model, verified.observed)
		}
	} else if target == "running" {
		_, actionError = worker.StartRuntime(ctx, client, verified.scope, verified.model, verified.observed)
	} else {
		_, actionError = worker.StopRuntime(ctx, client, verified.scope, verified.model, verified.observed)
	}
	terminal := initial
	observed, after, observationError := captureProcess(ctx, client, worker, verified)
	result.Resources = observed
	if observationError == nil {
		terminal.After = after
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		observationError = e
	}
	outcome, code := "uncertain", "OFFLINE_SCOPE_PROCESS_ACTION_UNCERTAIN"
	if observationError == nil && reflect.DeepEqual(terminal.After, initial.Before) && compose.ProcessNotSubmitted(actionError, verified.scope, verified.model, verified.observed, target == "running", selected) {
		outcome, code = "unsubmitted", "OFFLINE_SCOPE_PROCESS_ACTION_NOT_SUBMITTED"
		terminal.Submission = processSubmissionNotSubmitted
	} else if actionError == nil && observationError == nil {
		outcome, code = processOutcome(terminal, verified.model)
		if outcome == "unresolved" {
			outcome = "uncertain"
			code = "OFFLINE_SCOPE_PROCESS_ACTION_UNCERTAIN"
		}
	}
	if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: outcome, Code: code, Process: &terminal}); e != nil {
		return result, e
	}
	result.Pending = outcome == "uncertain"
	result.Status = "recorded_process_" + outcome
	if outcome != "completed" {
		return result, failure(code)
	}
	return result, nil
}
func captureProcess(ctx context.Context, client runtimeEngine, worker runtimeOperator, verified verifiedRuntimeContext) (compose.RuntimeStatus, map[string]ProcessSnapshot, error) {
	observed, e := worker.ObserveRuntime(ctx, client, verified.scope, verified.model)
	if e != nil {
		return observed, nil, e
	}
	if observed.Status != "observed_complete_compose_resources" || !runtimeIDsMatch(*verified.creation.record, observed) {
		return observed, nil, failure("OFFLINE_SCOPE_RECORDED_RUNTIME_MISSING_OR_CHANGED")
	}
	snapshots := map[string]ProcessSnapshot{}
	for name, status := range observed.Services {
		object, found, e := client.InspectRuntime(ctx, verified.scope.ScopeID, name)
		if e != nil {
			return observed, nil, e
		}
		if !found || object.ID != status.ID || object.State.Status != status.State || object.State.ExitCode != status.ExitCode {
			return observed, nil, failure("OFFLINE_SCOPE_PROCESS_OBSERVATION_CHANGED")
		}
		snapshots[name] = ProcessSnapshot{ID: object.ID, State: object.State.Status, ExitCode: object.State.ExitCode, StartedAt: object.State.StartedAt}
	}
	if !validProcessMap(snapshots, *verified.creation.record) {
		return observed, nil, failure("OFFLINE_SCOPE_PROCESS_OBSERVATION_INVALID")
	}
	return observed, snapshots, nil
}
func additionalProcessResources(model compose.Model, before map[string]ProcessSnapshot) (engine.ResourceRequirement, error) {
	return additionalSelectedProcessResources(model, before, nil)
}
func additionalSelectedProcessResources(model compose.Model, before map[string]ProcessSnapshot, selected []string) (engine.ResourceRequirement, error) {
	result := engine.ResourceRequirement{}
	for name, service := range model.Services {
		if len(selected) > 0 && name != selected[0] {
			continue
		}
		snapshot, ok := before[name]
		if !ok {
			return result, failure("OFFLINE_SCOPE_PROCESS_MODEL_CONTEXT_INVALID")
		}
		if snapshot.State == "running" {
			continue
		}
		if service.MemoryBytes <= 0 || service.MilliCPU <= 0 || result.MemoryBytes > math.MaxInt64-service.MemoryBytes || result.MilliCPU > math.MaxInt64-service.MilliCPU {
			return result, failure("OFFLINE_SCOPE_PROCESS_RESOURCE_REQUIREMENT_INVALID")
		}
		result.MemoryBytes += service.MemoryBytes
		result.MilliCPU += service.MilliCPU
	}
	if result.MemoryBytes <= 0 || result.MilliCPU <= 0 {
		return result, failure("OFFLINE_SCOPE_PROCESS_RESOURCE_REQUIREMENT_INVALID")
	}
	return result, nil
}
func processOutcome(r ProcessRecord, model compose.Model) (string, string) {
	if selectedProcessTarget(r.After, r.Target, r.SelectedServices) && nonselectedProcessUnchanged(r) {
		return "completed", ""
	}
	if observedStartFailure(r, &model) {
		return "failed", "OFFLINE_SCOPE_PROCESS_START_FAILED_OBSERVED"
	}
	return "unresolved", "OFFLINE_SCOPE_PROCESS_OUTCOME_UNRESOLVED"
}

const minimumProcessFilesystemFreeFraction = 0.30

func admitProcessJournal(l *locked, events int) error {
	journal, e := l.root.Stat(journalName)
	needed := int64(events * maximumEventBytes)
	if e != nil || journal.Size()+needed > l.maximumJournalBytes() {
		return failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	for _, path := range []string{"/", l.config.ScopeRoot} {
		var fs syscall.Statfs_t
		if syscall.Statfs(path, &fs) != nil || fs.Blocks == 0 || float64(fs.Bavail)*float64(fs.Bsize)-float64(needed) < float64(fs.Blocks)*float64(fs.Bsize)*minimumProcessFilesystemFreeFraction {
			return failure("OFFLINE_SCOPE_PROCESS_STORAGE_HEADROOM_REQUIRED")
		}
	}
	return nil
}

type RestartResult struct {
	Status        string         `json:"status"`
	Stopped       ProcessResult  `json:"stopped"`
	Started       *ProcessResult `json:"started,omitempty"`
	Qualification string         `json:"qualification"`
}

func RestartRuntime(ctx context.Context, c configuration.Config) (RestartResult, error) {
	worker, e := compose.New(c)
	if e != nil {
		return RestartResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestartResult{}, e
	}
	defer client.Close()
	return restartRuntime(ctx, c, client, worker)
}
func restartRuntime(ctx context.Context, c configuration.Config, client processEngine, worker processOperator) (RestartResult, error) {
	result := RestartResult{Status: "restart_stop_incomplete", Qualification: "separate_journaled_stop_start_process_sequence_not_application_readiness_or_target"}
	l, e := open(c, true)
	if e != nil {
		return result, e
	}
	defer l.close()
	result.Stopped, e = l.processLocked(ctx, client, worker, "stopped", false)
	if e != nil {
		return result, e
	}
	result.Status = "restart_start_incomplete"
	started, e := l.processLocked(ctx, client, worker, "running", false)
	result.Started = &started
	if e != nil {
		return result, e
	}
	result.Status = "recorded_restart_sequence_completed"
	return result, nil
}
