package scope

import (
	"bytes"
	"context"
	"os"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const bootstrapCopyName = "bootstrap-credential.json"
const maximumBootstrapCopyBytes = 16 << 10

func bootstrapKind(kind string) bool {
	return kind == engine.DatabaseBootstrap || kind == engine.DatabaseBootstrapReconcile || kind == engine.DatabaseBootstrapResume
}
func beforeDatabaseTaskCreation(events []Event, operation string) []Event {
	for i, e := range events {
		if e.Action == "database_task_create" && e.Outcome == "started" && e.DatabaseTask != nil && e.DatabaseTask.Task.Owner.OperationID == operation {
			return events[:i]
		}
	}
	return events
}
func bootstrapTransition(events []Event, next string) bool {
	s := databaseTaskState(events)
	a := databaseTaskActionState(events)
	if s.record == nil || s.pending != "" || !a.removed || !a.everExecuted || !a.succeeded && !a.failed || !runtimeAccessQualified(events) {
		return false
	}
	switch s.record.Task.Kind {
	case engine.DatabaseRuntimeCheck:
		return next == engine.DatabaseBootstrap && a.succeeded
	case engine.DatabaseBootstrap, engine.DatabaseBootstrapResume:
		return next == engine.DatabaseBootstrapReconcile
	case engine.DatabaseBootstrapReconcile:
		return next == engine.DatabaseBootstrapResume && a.failed
	}
	return false
}
func bootstrapPredecessorProof(events []Event, next string) (string, bool) {
	if !bootstrapTransition(events, next) {
		return "", false
	}
	return databaseTaskTerminalProof(events, databaseTaskState(events).record.Task.Kind, false)
}
func bootstrapQualified(events []Event) bool {
	_, ok := completedDatabaseTaskProof(events, engine.DatabaseBootstrapReconcile)
	return ok && runtimeAccessQualified(events) && bootstrapCopySHA(events) != ""
}
func bootstrapCopySHA(events []Event) string {
	for _, e := range events {
		if e.DatabaseTaskAction != nil && e.DatabaseTaskAction.BootstrapIntentSHA256 != "" {
			return e.DatabaseTaskAction.BootstrapIntentSHA256
		}
	}
	return ""
}
func (l *locked) verifyBootstrapCopy() error {
	expected := bootstrapCopySHA(l.events)
	_, e := l.root.Lstat(bootstrapCopyName)
	if expected == "" && os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_REQUIRED")
	}
	raw, e := regular(l.root, bootstrapCopyName, maximumBootstrapCopyBytes)
	if e != nil {
		return e
	}
	if expected != "" && wire.Digest(raw) != expected {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_CHANGED")
	}
	if expected == "" {
		s := databaseTaskState(l.events)
		if s.record == nil || !bootstrapKind(s.record.Task.Kind) {
			return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_UNEXPECTED")
		}
	}
	return nil
}
func (l *locked) persistBootstrapCopy(intent engine.BootstrapIntent) error {
	if intent.Bytes < 1 || intent.Bytes > maximumBootstrapCopyBytes || len(intent.Contents) != intent.Bytes || wire.Digest(intent.Contents) != intent.SHA256 {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_RESULT_INVALID")
	}
	if expected := bootstrapCopySHA(l.events); expected != "" && expected != intent.SHA256 {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_CHANGED")
	}
	_, e := l.root.Lstat(bootstrapCopyName)
	if os.IsNotExist(e) {
		if e = writeNew(l.root, bootstrapCopyName, intent.Contents); e != nil {
			return e
		}
	} else if e != nil {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_UNAVAILABLE")
	}
	d, e := l.root.Open(".")
	if e != nil {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_COPY_PARENT_UNAVAILABLE")
	}
	se := d.Sync()
	ce := d.Close()
	if se != nil || ce != nil {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_COPY_PARENT_SYNC_UNCERTAIN")
	}
	raw, e := regular(l.root, bootstrapCopyName, maximumBootstrapCopyBytes)
	if e != nil || !bytes.Equal(raw, intent.Contents) {
		return failure("OFFLINE_SCOPE_BOOTSTRAP_CREDENTIAL_COPY_CHANGED")
	}
	return nil
}
func CreateBootstrapTask(ctx context.Context, c configuration.Config, kind string, reconcile bool) (DatabaseTaskResult, error) {
	if !bootstrapKind(kind) {
		return DatabaseTaskResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return createDatabaseTaskClient(ctx, c, kind, reconcile)
}
func BootstrapTaskAction(ctx context.Context, c configuration.Config, kind, target string, reconcile bool) (DatabaseTaskActionResult, error) {
	if !bootstrapKind(kind) {
		return DatabaseTaskActionResult{}, failure("OFFLINE_SCOPE_DATABASE_TASK_KIND_INVALID")
	}
	return databaseTaskActionClient(ctx, c, target, reconcile, kind)
}
