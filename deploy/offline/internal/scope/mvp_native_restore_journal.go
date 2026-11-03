package scope

import (
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	mvpNativeRestoreSQLStage      = "sql"
	mvpNativeRestoreDatabaseStage = "database"
	mvpNativeRestoreStoresStage   = "stores"
)

// MVPNativeRestoreStageRecord selects the native protocol explicitly. Its
// immutable execution file is published and checked by the owning controller;
// a digest or this discriminator alone never grants task or cleanup authority.
// The original physical task IDs remain in completed task-create records.
// ResultSHA256 binds exact private execution/custody receipts instead of
// duplicating their potentially large task profiles and original output bytes.
// In particular, no native record asserts completion of legacy split effects.
type MVPNativeRestoreStageRecord struct {
	FormatVersion     int    `json:"format_version"`
	Stage             string `json:"stage"`
	ExecutionSHA256   string `json:"execution_sha256"`
	OriginalRestoreID string `json:"original_restore_id"`
}

func validMVPNativeRestoreStageRecord(r *MVPNativeRestoreStageRecord, stage, restoreID string) bool {
	return r != nil && r.FormatVersion == 1 && r.Stage == stage &&
		wire.ValidSHA256(r.ExecutionSHA256) && identityPattern.MatchString(r.OriginalRestoreID) &&
		r.OriginalRestoreID == restoreID
}

// Preparation may produce a final execution file after original target/root
// observations. Its exact private descriptor is bound by step zero's result.
// Reconciliation of any started step is inspection only: it must not create,
// start, publish, repair, remove, or adopt a replacement physical task.
func restoreSQLStepsForRecord(r *RestoreSQLRecord) []restoreSQLStep {
	if r == nil || r.Native == nil {
		return restoreSQLSteps()
	}
	return []restoreSQLStep{
		{"native-prepare", ""},
		{"task-create", "native-writer"},
		{"task-execute", "native-writer"},
		{"task-remove", "native-writer"},
		{"task-create", "native-observer"},
		{"task-execute", "native-observer"},
		{"task-remove", "native-observer"},
		{"complete", ""},
	}
}

func restoreDatabaseStepsForRecord(r *RestoreDatabaseRecord) []restoreDatabaseStep {
	if r == nil || r.Native == nil {
		return restoreDatabaseSteps()
	}
	return []restoreDatabaseStep{
		{"task-create", engine.NativePostgreSQLRestoreVerify},
		{"task-execute", engine.NativePostgreSQLRestoreVerify},
		{"task-remove", engine.NativePostgreSQLRestoreVerify},
		{"complete", ""},
	}
}

func restoreStoresStepsForRecord(r *RestoreStoresRecord) []restoreStoresStep {
	if r == nil || r.Native == nil {
		if r == nil {
			return restoreStoresSteps(nil)
		}
		return restoreStoresSteps(r.Stores)
	}
	return []restoreStoresStep{
		{"task-create", "native-observer", engine.NativePostgreSQLRestoreVerify},
		{"task-execute", "native-observer", engine.NativePostgreSQLRestoreVerify},
		{"task-remove", "native-observer", engine.NativePostgreSQLRestoreVerify},
		{"complete", "", ""},
	}
}

func nativeRestoreStepResultValid(phase, objectID, resultSHA256, observedSHA256 string) bool {
	switch phase {
	case "task-create":
		return utilityIDPattern.MatchString(objectID) && resultSHA256 == ""
	case "native-prepare", "task-execute":
		return objectID == "" && wire.ValidSHA256(resultSHA256)
	case "task-remove":
		return objectID == "" && resultSHA256 == ""
	case "complete":
		return objectID == "" && wire.ValidSHA256(resultSHA256) && resultSHA256 == observedSHA256
	default:
		return false
	}
}

func nativeRestoreSQLResultValid(r RestoreSQLRecord, events []Event) bool {
	observed := ""
	for _, prior := range restoreSQLState(events).completed {
		if prior.Native != nil && prior.Phase == "task-execute" && prior.Role == "native-observer" {
			observed = prior.ResultSHA256
		}
	}
	return nativeRestoreStepResultValid(r.Phase, r.ObjectID, r.ResultSHA256, observed)
}

func nativeRestoreDatabaseResultValid(r RestoreDatabaseRecord, events []Event) bool {
	observed := ""
	for _, prior := range restoreDatabaseState(events).completed {
		if prior.Native != nil && prior.Phase == "task-execute" && prior.Kind == engine.NativePostgreSQLRestoreVerify {
			observed = prior.ResultSHA256
		}
	}
	return nativeRestoreStepResultValid(r.Phase, r.ObjectID, r.ResultSHA256, observed)
}

func nativeRestoreStoresResultValid(r RestoreStoresRecord, events []Event) bool {
	observed := ""
	for _, prior := range restoreStoresState(events).completed {
		if prior.Native != nil && prior.Phase == "task-execute" && prior.Role == "native-observer" && prior.Kind == engine.NativePostgreSQLRestoreVerify {
			observed = prior.ResultSHA256
		}
	}
	return nativeRestoreStepResultValid(r.Phase, r.ObjectID, r.ResultSHA256, observed)
}
