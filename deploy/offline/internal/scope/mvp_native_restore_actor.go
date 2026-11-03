package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Reconciliation receives only original readonly SDK primitives. It cannot
// create, start, stop, remove or silently replace an uncertain physical actor.
type mvpNativeRestoreActorInspector interface {
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	ReadNativePostgreSQLTaskCompletion(context.Context, engine.DatabaseTask, string) (engine.TaskOutputObservation, error)
}

var _ mvpNativeRestoreActorInspector = (*engine.Client)(nil)

func mvpNativeRestoreActorExited(actor engine.RuntimeContainer, id string) bool {
	s := actor.State
	return actor.ID == id && s.Status == "exited" && !s.Running && !s.Paused && !s.Dead && !s.Restarting && !s.OOMKilled && s.ExitCode == 0
}

// A new container with the same owned name is a different physical operation.
// Its completion must never be adopted while reconciling the old recorded ID.
func inspectMVPNativeRestoreCompletion(ctx context.Context, client mvpNativeRestoreActorInspector, plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, task engine.DatabaseTask, recordedID string) (*engine.NativePostgreSQLRestoreCompletion, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if client == nil || !utilityIDPattern.MatchString(recordedID) || engine.ValidateNativePostgreSQLRecoveryTask(task) != nil || task.Kind != engine.NativePostgreSQLRestore || task.Owner.ScopeID != scopeID || task.Owner.OperationID != plan.AttemptID || task.NativePostgreSQLRecovery == nil || task.NativePostgreSQLRecovery.RestoreRequest == nil || !reflect.DeepEqual(*task.NativePostgreSQLRecovery.RestoreRequest, request) || !reflect.DeepEqual(task.NativePostgreSQLRecovery.BackupReference, plan.Source.NativePostgreSQL) {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CONTEXT_CHANGED")
	}
	actor, found, e := client.InspectDatabaseTask(ctx, task)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_UNRESOLVED")
	}
	if actor.ID != recordedID {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
	}
	if !mvpNativeRestoreActorExited(actor, recordedID) {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_UNRESOLVED")
	}
	output, e := client.ReadNativePostgreSQLTaskCompletion(ctx, task, recordedID)
	if e != nil {
		return nil, e
	}
	value := output.NativePostgreSQLRestoreCompletion
	if value == nil {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_COMPLETION_REQUIRED")
	}
	completion, e := bindMVPNativeRestoreCompletion(plan, scopeID, request, connection, archiveSHA256, *value)
	if e != nil {
		return nil, e
	}
	actor, found, e = client.InspectDatabaseTask(ctx, task)
	if e != nil || !found || !mvpNativeRestoreActorExited(actor, recordedID) {
		// Retain the already captured original bytes for private diagnostics.
		// The error still refuses qualification or any replacement/replay.
		return &completion, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
	}
	return &completion, nil
}

func bindMVPNativeRestoreCompletion(plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, value engine.NativePostgreSQLRestoreCompletion) (engine.NativePostgreSQLRestoreCompletion, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_COMPLETION_CHANGED")
	if !wire.ValidSHA256(archiveSHA256) || value.ArchiveSHA256 != archiveSHA256 || len(value.Original) == 0 || int64(len(value.Original)) > request.Policy.MaximumManifestBytes || value.OriginalSHA256 != wire.Digest(value.Original) {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	var decoded engine.NativePostgreSQLRestoreCompletion
	var fields map[string]json.RawMessage
	if wire.StrictJSON(value.Original, &decoded) != nil || wire.UniqueJSON(value.Original, &fields) != nil || !bytes.Equal(bytes.TrimSpace(fields["current_lock_held"]), []byte("false")) {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	public := value
	public.Original, public.OriginalSHA256 = nil, ""
	if !reflect.DeepEqual(decoded, public) || value.Format != "projectone.postgres.restore-complete.v1" || value.RestoreID != request.RestoreID || value.ManifestSHA256 != request.SourceManifestSHA256 || value.CurrentLockHeld || value.Qualification == "" {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	for _, hash := range []string{value.IntentSHA256, value.FilesSHA256, value.ArchiveSHA256, value.TargetBindingSHA256, value.PostDataSHA256} {
		if !wire.ValidSHA256(hash) {
			return engine.NativePostgreSQLRestoreCompletion{}, bad
		}
	}
	if _, e := time.Parse(time.RFC3339Nano, value.CompletedAt); e != nil {
		return engine.NativePostgreSQLRestoreCompletion{}, bad
	}
	if _, e := bindMVPNativeRestoreOriginalRuntimeContext(plan, scopeID, request, connection, value.ArchiveSHA256, value.RuntimeContext); e != nil {
		return engine.NativePostgreSQLRestoreCompletion{}, e
	}
	value.Original = append([]byte(nil), value.Original...)
	return value, nil
}
