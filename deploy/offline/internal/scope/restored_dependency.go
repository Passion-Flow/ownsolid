package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// RestoredDependencyAdmission authorizes only one existing PostgreSQL process.
// The bound TLS plan includes the independently verified private payload digest.
type RestoredDependencyAdmission struct {
	ReplacementProofSHA256  string `json:"replacement_proof_sha256"`
	TLSProofSHA256          string `json:"tls_proof_sha256"`
	EffectiveRuntimeSHA256  string `json:"effective_runtime_sha256"`
	ConfigurationPlanSHA256 string `json:"configuration_plan_sha256"`
}

func dependencyAdmission(events []Event) *RestoredDependencyAdmission {
	replacement, ok := controlReplacementProof(events)
	tls, tlsOK := tlsProjectionProof(events)
	runtime := effectiveRuntimeState(events)
	state := tlsProjectionState(events)
	replaced := controlReplacementState(events)
	if !ok || !tlsOK || state.record == nil || replaced.record == nil || replaced.record.GenerationID != state.record.GenerationID || runtime.record == nil || !runtime.completed || runtime.pending != "" {
		return nil
	}
	return &RestoredDependencyAdmission{ReplacementProofSHA256: replacement, TLSProofSHA256: tls, EffectiveRuntimeSHA256: wire.Digest(encode(*runtime.record)), ConfigurationPlanSHA256: state.record.PlanSHA256}
}

func validDependencyProcess(events []Event, record ProcessRecord) bool {
	if record.RestoredApplication != nil {
		return validApplicationProcess(events, record)
	}
	expected := dependencyAdmission(events)
	if record.RestoredDependency == nil {
		return record.Target != "running" || expected == nil
	}
	control, found := record.Before["control"]
	return expected != nil && record.Target == "running" && len(record.SelectedServices) == 1 && record.SelectedServices[0] == "postgres" && found && (control.State == "created" || control.State == "exited" && control.ExitCode == 0 && knownActivatedControl(events, control) || validStoppedRecoveryDependency(events, record)) && reflect.DeepEqual(expected, record.RestoredDependency)
}

func StartRestoredDatabase(ctx context.Context, c configuration.Config) (ProcessResult, error) {
	return startRestoredDatabase(ctx, c, false)
}

func RetryRestoredDatabase(ctx context.Context, c configuration.Config) (ProcessResult, error) {
	return startRestoredDatabase(ctx, c, true)
}

func startRestoredDatabase(ctx context.Context, c configuration.Config, retry bool) (ProcessResult, error) {
	if c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return ProcessResult{}, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ProcessResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return ProcessResult{}, e
	}
	l, e := open(c, true)
	if e != nil {
		return ProcessResult{}, e
	}
	defer l.close()
	return l.processSelectedAttemptLocked(ctx, client, worker, "running", false, []string{"postgres"}, restoredDependencyProcess, retry)
}

func (l *locked) admitRestoredDependency(ctx context.Context, client controlReplacementEngine, worker runtimeOperator) (*RestoredDependencyAdmission, error) {
	return l.admitRestoredDependencyAttempt(ctx, client, worker, "")
}

func (l *locked) admitRestoredDependencyAttempt(ctx context.Context, client controlReplacementEngine, worker runtimeOperator, retryOf string) (*RestoredDependencyAdmission, error) {
	return l.admitRestoredDependencyRecovery(ctx, client, worker, retryOf, nil)
}

func (l *locked) admitRestoredDependencyRecovery(ctx context.Context, client controlReplacementEngine, worker runtimeOperator, retryOf string, recovery *configuration.ProcessRecoveryRequest) (*RestoredDependencyAdmission, error) {
	if recovery != nil && recovery.Validate() != nil {
		return nil, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_REQUEST_INVALID")
	}
	admission := dependencyAdmission(l.events)
	if admission == nil {
		return nil, failure("OFFLINE_SCOPE_COMPLETED_CONTROL_REPLACEMENT_REQUIRED")
	}
	if processState(l.events).pending != "" {
		return nil, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	// Current PKI is verified for each new effect, never inferred from an old proof.
	tls, files, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		return nil, e
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		return nil, e
	}
	control, found := verified.observed.Services["control"]
	postgres, databaseFound := verified.observed.Services["postgres"]
	var recoveryContext stoppedControlRecovery
	failedControlAllowed := false
	if control.State == "exited" && control.ExitCode != 0 {
		_, current, inspectError := captureProcess(ctx, client, worker, verified)
		failedID := ""
		if recovery != nil {
			failedID = recovery.FailedControlStartOperationID
		}
		if inspectError == nil {
			recoveryContext, failedControlAllowed = stoppedRecoveryAtSnapshot(l.events, wire.Digest(encode(*verified.creation.record)), current, failedID, recovery == nil)
		}
	}
	if !found || control.State != "created" && control.State != "exited" || control.ExitCode != 0 && !failedControlAllowed || !databaseFound || postgres.State != "created" && postgres.State != "exited" && postgres.State != "running" || postgres.ExitCode != 0 && retryOf == "" {
		return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_STOPPED_CONTROL_REQUIRED")
	}
	if retryOf != "" {
		_, before, inspectError := captureProcess(ctx, client, worker, verified)
		r := ProcessRecord{AttemptVersion: processAttemptVersion, RetryOfOperationID: retryOf, CreationSHA256: wire.Digest(encode(*verified.creation.record)), Target: "running", SelectedServices: []string{"postgres"}, Before: before}
		if recovery != nil {
			r.AttemptVersion, r.RecoveryOfOperationID, r.RecoveryStopOperationID, r.RecoveryReason = processRecoveryAttemptVersion, recovery.FailedControlStartOperationID, recoveryContext.stopID, recovery.Reason
			r.RestoredDependency = admission
		}
		if inspectError != nil || !validProcessRetry(l.events, r) {
			return nil, failure("OFFLINE_SCOPE_PROCESS_RETRY_PREDECESSOR_CHANGED")
		}
	}
	for name, service := range verified.observed.Services {
		if name != "postgres" && (service.State != "created" && service.State != "exited" || service.ExitCode != 0 && !(name == "control" && failedControlAllowed)) {
			return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_STOPPED_PEERS_REQUIRED")
		}
	}
	destination := ""
	for _, mount := range verified.model.Services["control"].Mounts {
		if mount.Source == tls.ConfigurationOwner.Role {
			if destination != "" || !mount.ReadOnly {
				return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_MOUNT_REQUIRED")
			}
			destination = mount.Target
		}
	}
	if destination == "" {
		return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_MOUNT_REQUIRED")
	}
	read := client.InspectStoppedControlConfiguration
	if failedControlAllowed {
		failed, supported := client.(failedControlConfigurationReader)
		if !supported {
			return nil, failure("OFFLINE_SCOPE_FAILED_CONTROL_CONFIGURATION_READBACK_REQUIRED")
		}
		read = failed.InspectFailedControlConfiguration
	} else if control.State == "exited" {
		object, present, inspectError := client.InspectRuntime(ctx, l.marker.ScopeID, "control")
		current, supported := client.(currentControlConfigurationReader)
		if inspectError != nil || !present || object.ID != control.ID || object.State.Status != "exited" || object.State.ExitCode != 0 || !knownActivatedControl(l.events, ProcessSnapshot{ID: control.ID, State: control.State, ExitCode: control.ExitCode, StartedAt: object.State.StartedAt}) || !supported {
			return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_KNOWN_CONTROL_REQUIRED")
		}
		read = current.InspectCurrentControlConfiguration
	}
	observed, e := read(ctx, tls.ConfigurationOwner, control.ID, destination, tls.UID, tls.GID, files)
	if e != nil || observed.Status != engine.ConfigurationPayloadVerified {
		return nil, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_READBACK_REQUIRED")
	}
	return admission, nil
}
