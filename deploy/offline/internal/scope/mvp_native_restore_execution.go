package scope

import (
	"context"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The caller must durably record the original create ID and execution intent
// before entering this runner. It has no create, stop, remove or journal method;
// output/custody and later explicit cleanup remain separate owning steps.
type mvpNativeRestoreExecutionClient interface {
	mvpNativeRestoreActorInspector
	StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
}

var _ mvpNativeRestoreExecutionClient = (*engine.Client)(nil)

type mvpNativeRestoreOutput interface {
	Wait(context.Context) (engine.TaskOutputObservation, error)
	Close() error
}

type mvpNativeRestoreAttach func(context.Context, engine.DatabaseTask, string) (mvpNativeRestoreOutput, error)

func mvpNativeRestoreSDKAttachment(client *engine.Client) mvpNativeRestoreAttach {
	return func(ctx context.Context, task engine.DatabaseTask, id string) (mvpNativeRestoreOutput, error) {
		if client == nil {
			return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_CLIENT_REQUIRED")
		}
		return client.ObserveDatabaseTaskOutput(ctx, task, id)
	}
}

// Execute exactly one ORIGINAL full native command. This is not the old split
// SQL/schema/migration/runtime/store/invalidation loop. A start error or unknown
// state is returned without a second start, replacement, stop or cleanup. The
// readonly completion inspector is the separate reconciliation primitive.
func executeMVPNativeRestoreOriginalTask(ctx context.Context, client mvpNativeRestoreExecutionClient, attach mvpNativeRestoreAttach, policy configuration.Compose, plan RestorePlan, scopeID string, request wire.PostgreSQLRestoreRequest, connection restoreConnection, archiveSHA256 string, task engine.DatabaseTask, recordedID string, files []engine.ConfigurationFile) (engine.TaskOutputObservation, *engine.NativePostgreSQLRestoreCompletion, error) {
	bad := failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_CONTEXT_CHANGED")
	if ctx == nil || ctx.Err() != nil {
		return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if client == nil || attach == nil || policy.Process == nil || policy.Validate() != nil || !mvpNativeRestoreRequestContext(plan, scopeID, request, connection, archiveSHA256) || len(request.TargetRoots) != 4 || !utilityIDPattern.MatchString(recordedID) || engine.ValidateNativePostgreSQLRecoveryTask(task) != nil || task.Kind != engine.NativePostgreSQLRestore || task.Owner.ScopeID != scopeID || task.Owner.OperationID != plan.AttemptID || task.NativePostgreSQLRecovery == nil || task.NativePostgreSQLRecovery.RestoreRequest == nil || !reflect.DeepEqual(*task.NativePostgreSQLRecovery.RestoreRequest, request) || !reflect.DeepEqual(task.NativePostgreSQLRecovery.BackupReference, plan.Source.NativePostgreSQL) {
		return engine.TaskOutputObservation{}, nil, bad
	}
	process := *policy.Process
	bounded, cancel := context.WithTimeout(ctx, time.Duration(process.ObserveSeconds)*time.Second)
	defer cancel()
	actor, found, e := client.InspectDatabaseTask(bounded, task)
	if e != nil {
		return engine.TaskOutputObservation{}, nil, e
	}
	if !found || actor.ID != recordedID {
		return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
	}
	started, te := time.Parse(time.RFC3339Nano, actor.State.StartedAt)
	if te != nil || !started.IsZero() || actor.State.Status != "created" || actor.State.Running || actor.State.Dead || actor.State.Paused || actor.State.Restarting || actor.State.OOMKilled || actor.State.ExitCode != 0 {
		return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_NEVER_STARTED_REQUIRED")
	}
	observer, e := attach(bounded, task, recordedID)
	if e != nil {
		return engine.TaskOutputObservation{}, nil, e
	}
	if observer == nil {
		return engine.TaskOutputObservation{}, nil, bad
	}
	defer observer.Close()
	// Attach occurs before this one physical start, so no original stdout is
	// replaced by a later sanitized message or manufactured completion file.
	if _, e = client.StartDatabaseTask(bounded, task, recordedID, files, process.ReservePercent); e != nil {
		return engine.TaskOutputObservation{}, nil, e
	}
	for {
		if bounded.Err() != nil {
			return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_UNRESOLVED")
		}
		actor, found, e = client.InspectDatabaseTask(bounded, task)
		if e != nil {
			return engine.TaskOutputObservation{}, nil, e
		}
		if !found || actor.ID != recordedID {
			return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
		}
		if actor.State.OOMKilled || actor.State.Dead || actor.State.Paused || actor.State.Restarting || actor.State.Status == "exited" && actor.State.ExitCode != 0 {
			return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TASK_FAILED")
		}
		if mvpNativeRestoreActorExited(actor, recordedID) {
			break
		}
		if actor.State.Status != "running" || !actor.State.Running {
			return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_UNRESOLVED")
		}
		poll := time.NewTimer(time.Duration(process.PollMilliseconds) * time.Millisecond)
		select {
		case <-bounded.Done():
			poll.Stop()
			return engine.TaskOutputObservation{}, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_EXECUTION_UNRESOLVED")
		case <-poll.C:
		}
	}
	output, e := observer.Wait(bounded)
	if e != nil {
		return output, nil, e
	}
	if output.Redacted || output.StdoutCode != "" || output.StderrCode != "" || output.NativePostgreSQLRestoreCompletion == nil || output.NativePostgreSQLBackupCompletion != nil || output.NativePostgreSQLRestore != nil || output.NativePostgreSQLBackup != nil {
		return output, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_STDOUT_REQUIRED")
	}
	stdout, e := bindMVPNativeRestoreCompletion(plan, scopeID, request, connection, archiveSHA256, *output.NativePostgreSQLRestoreCompletion)
	if e != nil || output.Bytes < len(stdout.Original) || int64(output.Bytes) > request.Policy.MaximumManifestBytes {
		return output, nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_STDOUT_CHANGED")
	}
	output.NativePostgreSQLRestoreCompletion = &stdout
	complete, e := inspectMVPNativeRestoreCompletion(bounded, client, plan, scopeID, request, connection, archiveSHA256, task, recordedID)
	if e != nil {
		return output, complete, e
	}
	// Preserve stdout and file bytes separately. Equality of their decoded
	// original fields is required; one raw digest never impersonates the other.
	a, b := stdout, *complete
	a.Original, a.OriginalSHA256, b.Original, b.OriginalSHA256 = nil, "", nil, ""
	if !reflect.DeepEqual(a, b) {
		return output, complete, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_COMPLETIONS_DIFFER")
	}
	return output, complete, nil
}
