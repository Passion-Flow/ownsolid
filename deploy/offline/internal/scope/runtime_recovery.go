package scope

import (
	"context"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const runtimeAbsenceRecoveryAction = "runtime_recover_absent"

type RuntimeAbsenceRecoveryResult struct {
	Status                   string `json:"status"`
	ScopeID                  string `json:"scope_id"`
	RuntimeCreateOperationID string `json:"runtime_create_operation_id"`
	RecoveryOperationID      string `json:"recovery_operation_id"`
	ProofSHA256              string `json:"proof_sha256"`
	Qualification            string `json:"qualification"`
}

func runtimeRecordAbsent(r RuntimeRecord) bool {
	return r.NetworkID == "" && r.IngressNetworkID == "" && len(r.Services) == 0
}

func runtimeObservationAbsent(r compose.RuntimeStatus) bool {
	return r.Status == "observed_absent_compose_resources" && r.NetworkID == "" && r.IngressNetworkID == "" && len(r.Services) == 0
}

func runtimeRecoveryInitialHistory(events []Event) bool {
	for _, e := range events {
		if e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil || e.Backup != nil || e.Restore != nil || e.RestoreSQL != nil || e.RestoreDatabase != nil || e.RestoreStores != nil || e.TLSProjection != nil || e.ControlReplacement != nil || e.RestoredLiveDatabase != nil || e.RestoredCurrentStores != nil || e.CumulativeUpgrade != nil {
			return false
		}
	}
	return true
}

func runtimeAbsenceResult(scope string, e Event) RuntimeAbsenceRecoveryResult {
	return RuntimeAbsenceRecoveryResult{Status: "runtime_absence_recovery_recorded", ScopeID: scope, RuntimeCreateOperationID: e.ReferenceOperationID, RecoveryOperationID: e.OperationID, ProofSHA256: wire.Digest(encode(e)), Qualification: "retained_absence_record_only_not_historical_effect_absence_current_health_business_or_target_qualification"}
}

func RecoverAbsentRuntime(ctx context.Context, c configuration.Config, request configuration.RuntimeAbsenceRecoveryRequest) (RuntimeAbsenceRecoveryResult, error) {
	if request.Validate() != nil || c.Engine == nil || c.Compose == nil {
		return RuntimeAbsenceRecoveryResult{}, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RuntimeAbsenceRecoveryResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RuntimeAbsenceRecoveryResult{}, e
	}
	return recoverAbsentRuntime(ctx, c, request, client, worker)
}

func recoverAbsentRuntime(ctx context.Context, c configuration.Config, request configuration.RuntimeAbsenceRecoveryRequest, client runtimeEngine, worker runtimeOperator) (RuntimeAbsenceRecoveryResult, error) {
	var out RuntimeAbsenceRecoveryResult
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if request.Validate() != nil {
		return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_CONFIGURATION_REQUIRED")
	}
	l, e := open(c, true)
	if e != nil {
		return out, e
	}
	defer l.close()
	if request.ScopeID != l.marker.ScopeID {
		return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_SCOPE_MISMATCH")
	}
	digest := wire.Digest(encode(request))
	for _, prior := range l.events {
		if prior.Action == runtimeAbsenceRecoveryAction && prior.ReferenceOperationID == request.RuntimeCreateOperationID {
			if prior.Runtime.RecoverySHA256 != digest {
				return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_REQUEST_CHANGED")
			}
			return runtimeAbsenceResult(l.marker.ScopeID, prior), nil
		}
	}
	state := runtimeState(l.events)
	if state.pending != request.RuntimeCreateOperationID || state.record == nil || state.completed || !runtimeRecordAbsent(*state.record) || !runtimeRecoveryInitialHistory(l.events) {
		return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_PENDING_EMPTY_CREATION_REQUIRED")
	}
	verified, e := l.verifyRuntimeContext(ctx, client, worker, true)
	if e != nil {
		return out, e
	}
	if !runtimeObservationAbsent(verified.observed) {
		return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_RESOURCES_PRESERVED")
	}
	second, e := worker.ObserveRuntime(ctx, client, verified.scope, verified.model)
	if e != nil {
		return out, e
	}
	if !runtimeObservationAbsent(second) {
		return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_RESOURCES_PRESERVED")
	}
	for _, role := range volumeState(l.events).roles {
		owner := l.volumeOwner(role)
		if _, found, e := client.InspectVolume(ctx, owner); e != nil {
			return out, e
		} else if !found {
			return out, failure("OFFLINE_SCOPE_RUNTIME_RECOVERY_PREPARED_VOLUME_CHANGED")
		}
		if e = client.VerifyVolumeConsumers(ctx, owner, nil); e != nil {
			return out, e
		}
	}
	if _, e = l.checkEngine(ctx, client); e != nil {
		return out, e
	}
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	id, e := nonce()
	if e != nil {
		return out, e
	}
	record := RuntimeRecord{DescriptorSHA256: state.record.DescriptorSHA256, ModelSHA256: state.record.ModelSHA256, RecoverySHA256: digest}
	event := Event{OperationID: id, Action: runtimeAbsenceRecoveryAction, Outcome: "completed", ReferenceOperationID: state.pending, Runtime: &record}
	if e = l.appendEvent(event); e != nil {
		return out, e
	}
	return runtimeAbsenceResult(l.marker.ScopeID, l.events[len(l.events)-1]), nil
}
