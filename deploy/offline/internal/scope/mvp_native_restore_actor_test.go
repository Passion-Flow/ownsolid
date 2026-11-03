package scope

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// These inspector doubles expose only the original read methods. No task
// creation, execution, removal, SQL or target initialization is simulated.
type mvpNativeRestoreInspectorFixture struct {
	actor                         engine.RuntimeContainer
	found                         bool
	output                        engine.TaskOutputObservation
	inspectError, completionError error
	inspections, reads            int
	after                         func(*mvpNativeRestoreInspectorFixture)
}

func (f *mvpNativeRestoreInspectorFixture) InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	f.inspections++
	if f.inspections == 2 && f.after != nil {
		f.after(f)
	}
	return f.actor, f.found, f.inspectError
}

func (f *mvpNativeRestoreInspectorFixture) ReadNativePostgreSQLTaskCompletion(context.Context, engine.DatabaseTask, string) (engine.TaskOutputObservation, error) {
	f.reads++
	return f.output, f.completionError
}

func mvpNativeRestoreActorFixture(t *testing.T) (RestorePlan, string, wire.PostgreSQLRestoreRequest, restoreConnection, string, engine.DatabaseTask, string, *mvpNativeRestoreInspectorFixture) {
	t.Helper()
	plan, scopeID, request, connection, archive, runtime, _ := mvpNativeRestoreRuntimeFixture()
	// The SDK mounts the original backup's parent, so it must not be an
	// ancestor of the independent configuration, stores, records or scratch.
	request.BackupRoot = "/synthetic/backups/original-native-backup"
	own := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: scopeID, OperationID: plan.AttemptID, Role: role + "-" + plan.AttemptID}
	}
	records := own(engine.NativePostgreSQLRestoreRecordsRole)
	p := &engine.NativePostgreSQLRecoveryProfile{RestoreRequest: &request, BackupReference: plan.Source.NativePostgreSQL, ConfigurationOwner: own(engine.NativePostgreSQLConfigurationRole), BackupOwner: own(engine.NativePostgreSQLArtifactRole), ScratchOwner: own(engine.NativePostgreSQLRecoveryScratchRole), RecordsOwner: &records}
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		p.Stores[i] = engine.NativePostgreSQLStoreMount{Owner: engine.Ownership{ScopeID: scopeID, OperationID: strings.Repeat("5", 32), Role: role}, Target: "/synthetic/stores/" + role}
	}
	p.Registry = &engine.NativePostgreSQLRegistryMount{Owner: engine.Ownership{ScopeID: scopeID, OperationID: strings.Repeat("5", 32), Role: engine.NativePostgreSQLRegistryRole}, Target: "/synthetic/registry-base/cas"}
	p.Scaffold = &engine.NativePostgreSQLVerifierPlaceholder{Owner: own(engine.NativePostgreSQLScaffoldRole), Target: "/synthetic/registry-base"}
	task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: scopeID, OperationID: plan.AttemptID, Role: "database-" + engine.NativePostgreSQLRestore}, VolumeOwner: own(engine.NativePostgreSQLRecoveryInputRole), Kind: engine.NativePostgreSQLRestore, Image: "sha256:" + strings.Repeat("a", 64), UID: 65534, GID: 65534, NetworkID: strings.Repeat("7", 64), Peers: map[string]string{"postgres": strings.Repeat("8", 64), "control": strings.Repeat("9", 64)}, NativePostgreSQLRecovery: p}
	if e := engine.ValidateNativePostgreSQLRecoveryTask(task); e != nil {
		t.Fatal("invalid synthetic original actor", e)
	}
	complete := engine.NativePostgreSQLRestoreCompletion{Format: "projectone.postgres.restore-complete.v1", RestoreID: request.RestoreID, IntentSHA256: strings.Repeat("1", 64), FilesSHA256: strings.Repeat("2", 64), ManifestSHA256: request.SourceManifestSHA256, ArchiveSHA256: archive, TargetBindingSHA256: strings.Repeat("4", 64), PostDataSHA256: strings.Repeat("5", 64), RuntimeContext: runtime, CompletedAt: "2026-11-03T00:00:00Z", Qualification: "Synthetic original completion; no native restore executed"}
	complete.Original = encode(complete)
	complete.OriginalSHA256 = wire.Digest(complete.Original)
	id := strings.Repeat("c", 64)
	f := &mvpNativeRestoreInspectorFixture{found: true, output: engine.TaskOutputObservation{NativePostgreSQLRestoreCompletion: &complete}}
	f.actor.ID = id
	f.actor.State.Status = "exited"
	return plan, scopeID, request, connection, archive, task, id, f
}

func TestMVPNativeRestoreCompletionInspectsExactOriginalActorAndClonesBytes(t *testing.T) {
	plan, scopeID, request, connection, archive, task, id, f := mvpNativeRestoreActorFixture(t)
	got, e := inspectMVPNativeRestoreCompletion(context.Background(), f, plan, scopeID, request, connection, archive, task, id)
	if e != nil || got == nil || f.inspections != 2 || f.reads != 1 || !reflect.DeepEqual(*got, *f.output.NativePostgreSQLRestoreCompletion) {
		t.Fatal("original completion not bound", e)
	}
	f.output.NativePostgreSQLRestoreCompletion.Original[0] = '!'
	if got.Original[0] == '!' {
		t.Fatal("original bytes alias SDK output")
	}
}

func TestMVPNativeRestoreCompletionNeverReadsMissingUncertainOrReplacementActor(t *testing.T) {
	for _, name := range []string{"missing", "replaced", "created", "running", "paused", "dead", "restarting", "oom", "failed", "inspect-error"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, request, connection, archive, task, id, f := mvpNativeRestoreActorFixture(t)
			switch name {
			case "missing":
				f.found = false
			case "replaced":
				f.actor.ID = strings.Repeat("d", 64)
			case "created":
				f.actor.State.Status = "created"
			case "running":
				f.actor.State.Running = true
			case "paused":
				f.actor.State.Paused = true
			case "dead":
				f.actor.State.Dead = true
			case "restarting":
				f.actor.State.Restarting = true
			case "oom":
				f.actor.State.OOMKilled = true
			case "failed":
				f.actor.State.ExitCode = 1
			case "inspect-error":
				f.inspectError = errors.New("synthetic inspect failure")
			}
			if got, e := inspectMVPNativeRestoreCompletion(context.Background(), f, plan, scopeID, request, connection, archive, task, id); e == nil || got != nil || f.reads != 0 {
				t.Fatal("uncertain actor completion adopted")
			}
		})
	}
}

func TestMVPNativeRestoreCompletionRetainsCaptureButRefusesChangedActorAfterRead(t *testing.T) {
	for _, name := range []string{"missing", "replaced", "failed", "inspect-error"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, request, connection, archive, task, id, f := mvpNativeRestoreActorFixture(t)
			f.after = func(f *mvpNativeRestoreInspectorFixture) {
				switch name {
				case "missing":
					f.found = false
				case "replaced":
					f.actor.ID = strings.Repeat("d", 64)
				case "failed":
					f.actor.State.ExitCode = 1
				case "inspect-error":
					f.inspectError = errors.New("synthetic second inspect failure")
				}
			}
			got, e := inspectMVPNativeRestoreCompletion(context.Background(), f, plan, scopeID, request, connection, archive, task, id)
			if e == nil || got == nil || f.inspections != 2 || f.reads != 1 || !bytes.Equal(got.Original, f.output.NativePostgreSQLRestoreCompletion.Original) {
				t.Fatal("changed actor qualified or original diagnostic lost")
			}
		})
	}
}

func TestMVPNativeRestoreCompletionRejectsMissingAndChangedOriginal(t *testing.T) {
	for _, name := range []string{"missing", "getter-error", "no-bytes", "bad-sha", "extra-json", "changed-public", "foreign-context", "candidate", "lock-held", "time", "digest", "too-large", "changed-source-archive", "missing-lock-field", "null-lock-field"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, request, connection, archive, task, id, f := mvpNativeRestoreActorFixture(t)
			c := f.output.NativePostgreSQLRestoreCompletion
			switch name {
			case "missing":
				f.output.NativePostgreSQLRestoreCompletion = nil
			case "getter-error":
				f.completionError = errors.New("synthetic missing completion")
			case "no-bytes":
				c.Original = nil
			case "bad-sha":
				c.OriginalSHA256 = strings.Repeat("0", 64)
			case "extra-json":
				c.Original = append(c.Original, c.Original...)
			case "changed-public":
				c.CompletedAt = "2026-11-03T00:01:00Z"
			case "missing-lock-field":
				c.Original = bytes.Replace(c.Original, []byte(`"current_lock_held":false,`), nil, 1)
			case "null-lock-field":
				c.Original = bytes.Replace(c.Original, []byte(`"current_lock_held":false`), []byte(`"current_lock_held":null`), 1)
			case "foreign-context":
				c.RuntimeContext.TargetScopeID = strings.Repeat("f", 32)
				c.Original = encode(c)
			case "candidate":
				c.ManifestSHA256 = plan.Source.ManifestSHA256
				c.Original = encode(c)
			case "lock-held":
				c.CurrentLockHeld = true
				c.Original = encode(c)
			case "time":
				c.CompletedAt = "invalid"
				c.Original = encode(c)
			case "digest":
				c.PostDataSHA256 = ""
				c.Original = encode(c)
			case "changed-source-archive":
				archive = strings.Repeat("0", 64)
			case "too-large":
				c.Original = append(bytes.Repeat([]byte(" "), int(request.Policy.MaximumManifestBytes)), c.Original...)
			}
			if name != "bad-sha" && c != nil {
				c.OriginalSHA256 = wire.Digest(c.Original)
			}
			if got, e := inspectMVPNativeRestoreCompletion(context.Background(), f, plan, scopeID, request, connection, archive, task, id); e == nil || got != nil || f.inspections != 1 || f.reads != 1 {
				t.Fatal("missing/changed original accepted", e)
			}
		})
	}
}

func TestMVPNativeRestoreCompletionRejectsReboundTaskBeforeInspection(t *testing.T) {
	for _, name := range []string{"invalid-id", "foreign-scope", "foreign-attempt", "wrong-kind", "changed-request", "changed-source", "canceled", "nil-client"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, request, connection, archive, task, id, f := mvpNativeRestoreActorFixture(t)
			ctx := context.Background()
			var client mvpNativeRestoreActorInspector = f
			switch name {
			case "invalid-id":
				id = "not-a-physical-id"
			case "foreign-scope":
				scopeID = strings.Repeat("f", 32)
			case "foreign-attempt":
				plan.AttemptID = strings.Repeat("f", 32)
			case "wrong-kind":
				task.Kind = engine.NativePostgreSQLRestoreVerify
				task.Owner.Role = "database-" + task.Kind
			case "changed-request":
				request.Policy.OperationSeconds++
			case "changed-source":
				b := *plan.Source.NativePostgreSQL
				b.CompletionSHA256 = strings.Repeat("0", 64)
				plan.Source.NativePostgreSQL = &b
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil-client":
				client = nil
			}
			if got, e := inspectMVPNativeRestoreCompletion(ctx, client, plan, scopeID, request, connection, archive, task, id); e == nil || got != nil || f.inspections != 0 || f.reads != 0 {
				t.Fatal("rebound task inspected")
			}
		})
	}
}
