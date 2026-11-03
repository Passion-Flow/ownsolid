package scope

import (
	"context"
	"reflect"
	"slices"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ResumeRestoredDatabase starts only the existing dependency after a proven
// control failure and owned safe shutdown. It never restores or migrates data.
func ResumeRestoredDatabase(ctx context.Context, c configuration.Config, request configuration.ProcessRecoveryRequest) (ProcessResult, error) {
	return resumeStoppedControl(ctx, c, request, restoredDependencyProcess)
}

func ResumeRestoredApplication(ctx context.Context, c configuration.Config, request configuration.ProcessRecoveryRequest) (ProcessResult, error) {
	return resumeStoppedControl(ctx, c, request, restoredApplicationProcess)
}

func resumeStoppedControl(ctx context.Context, c configuration.Config, request configuration.ProcessRecoveryRequest, mode restoredProcessMode) (ProcessResult, error) {
	if request.Validate() != nil || c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return ProcessResult{}, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_CONFIGURATION_REQUIRED")
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
	return l.resumeStoppedControlLocked(ctx, client, worker, request, mode)
}

func (l *locked) resumeStoppedControlLocked(ctx context.Context, client processEngine, worker processOperator, request configuration.ProcessRecoveryRequest, mode restoredProcessMode) (ProcessResult, error) {
	if request.Validate() != nil || mode != restoredDependencyProcess && mode != restoredApplicationProcess {
		return ProcessResult{}, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_CONFIGURATION_REQUIRED")
	}
	if processState(l.events).pending != "" {
		return ProcessResult{Pending: true}, failure("OFFLINE_SCOPE_PROCESS_RECONCILIATION_REQUIRED")
	}
	selected, retry := "control", true
	if mode == restoredDependencyProcess {
		selected, retry = "postgres", false
		creation := effectiveRuntimeState(l.events)
		if creation.record == nil {
			return ProcessResult{}, failure("OFFLINE_SCOPE_COMPLETED_RUNTIME_CREATION_REQUIRED")
		}
		retry = retryableStartID(l.events, wire.Digest(encode(*creation.record)), []string{selected}) != ""
	}
	// A repeat of the exact completed recovery request is observation only.
	// Never dispatch a start while deciding whether an old result still holds.
	for i := len(l.events) - 1; i >= 0; i-- {
		event := l.events[i]
		record := event.Process
		if record == nil || record.Target != "running" || !slices.Equal(record.SelectedServices, []string{selected}) {
			continue
		}
		if event.Outcome == "completed" && record.AttemptVersion == processRecoveryAttemptVersion && record.RecoveryOfOperationID == request.FailedControlStartOperationID && record.RecoveryReason == request.Reason {
			verified, inspectError := l.verifyRuntimeContext(ctx, client, worker, false)
			if inspectError != nil {
				return ProcessResult{}, inspectError
			}
			observed, current, inspectError := captureProcess(ctx, client, worker, verified)
			if inspectError != nil {
				return ProcessResult{}, inspectError
			}
			if reflect.DeepEqual(current, record.After) && selectedProcessTarget(current, "running", []string{selected}) {
				if mode == restoredDependencyProcess {
					sdk, ok := client.(controlReplacementEngine)
					if !ok {
						return ProcessResult{}, failure("OFFLINE_SCOPE_RESTORED_DEPENDENCY_CONFIGURATION_READBACK_REQUIRED")
					}
					_, inspectError = l.admitRestoredDependency(ctx, sdk, worker)
				} else {
					sdk, ok := client.(restoredApplicationEngine)
					if !ok {
						return ProcessResult{}, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_READBACK_REQUIRED")
					}
					_, inspectError = l.admitRestoredApplication(ctx, sdk, worker)
				}
				if inspectError != nil {
					return ProcessResult{}, inspectError
				}
				return ProcessResult{ScopeID: l.marker.ScopeID, Status: "observed_completed_recovery_running", Resources: observed, Qualification: "current_exact_recovery_process_observation_only_not_application_health_or_target"}, nil
			}
		}
		break
	}
	return l.processSelectedRecoveryAttemptLocked(ctx, client, worker, "running", false, []string{selected}, mode, retry, &request)
}
