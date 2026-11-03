package scope

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type mvpNativeExecutionDouble struct {
	inspector               *mvpNativeRestoreInspectorFixture
	output                  *mvpNativeExecutionOutput
	events                  []string
	starts, attaches        int
	attached                bool
	startError, attachError error
	started                 func(*mvpNativeExecutionDouble)
}

type mvpNativeExecutionOutput struct {
	owner         *mvpNativeExecutionDouble
	value         engine.TaskOutputObservation
	err           error
	waits, closes int
}

func (o *mvpNativeExecutionOutput) Wait(context.Context) (engine.TaskOutputObservation, error) {
	o.waits++
	o.owner.events = append(o.owner.events, "wait")
	return o.value, o.err
}
func (o *mvpNativeExecutionOutput) Close() error {
	o.closes++
	o.owner.events = append(o.owner.events, "close")
	return nil
}
func (f *mvpNativeExecutionDouble) InspectDatabaseTask(ctx context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	f.events = append(f.events, "inspect")
	return f.inspector.InspectDatabaseTask(ctx, t)
}
func (f *mvpNativeExecutionDouble) ReadNativePostgreSQLTaskCompletion(ctx context.Context, t engine.DatabaseTask, id string) (engine.TaskOutputObservation, error) {
	f.events = append(f.events, "complete")
	return f.inspector.ReadNativePostgreSQLTaskCompletion(ctx, t, id)
}
func (f *mvpNativeExecutionDouble) StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error) {
	f.starts++
	f.events = append(f.events, "start")
	if !f.attached {
		return engine.ResourceAdmission{}, errors.New("synthetic start before original output attachment")
	}
	f.inspector.actor.State.Status = "exited"
	f.inspector.actor.State.StartedAt = "2026-11-03T00:00:00Z"
	if f.started != nil {
		f.started(f)
	}
	return engine.ResourceAdmission{}, f.startError
}
func (f *mvpNativeExecutionDouble) attach(context.Context, engine.DatabaseTask, string) (mvpNativeRestoreOutput, error) {
	f.attaches++
	f.events = append(f.events, "attach")
	if f.attachError != nil {
		return nil, f.attachError
	}
	f.attached = true
	return f.output, nil
}

func mvpNativeExecutionFixture(t *testing.T) (RestorePlan, string, wire.PostgreSQLRestoreRequest, restoreConnection, string, engine.DatabaseTask, string, configuration.Compose, *mvpNativeExecutionDouble) {
	t.Helper()
	plan, scopeID, request, connection, archive, task, id, inspector := mvpNativeRestoreActorFixture(t)
	for i, s := range task.NativePostgreSQLRecovery.Stores {
		request.TargetRoots = append(request.TargetRoots, wire.PostgreSQLRestoreRoot{Path: s.Target, Device: 1, Inode: uint64(20 + i), UID: uint32(task.UID), GID: uint32(task.GID)})
	}
	task.NativePostgreSQLRecovery.RestoreRequest = &request
	inspector.actor.State.Status = "created"
	inspector.actor.State.StartedAt = "0001-01-01T00:00:00Z"
	var process wire.ProcessPolicy
	if wire.StrictJSON(wire.ProcessProfile, &process) != nil {
		t.Fatal("process contract")
	}
	policy := configuration.Compose{DockerBinary: "/synthetic/docker", PluginDirectory: "/synthetic/plugins", CommandSeconds: 30, OutputBytes: 1 << 20, Process: &process.Defaults}
	if policy.Validate() != nil {
		t.Fatal("synthetic typed policy")
	}
	f := &mvpNativeExecutionDouble{inspector: inspector}
	stdout := *inspector.output.NativePostgreSQLRestoreCompletion
	stdout.Original = append([]byte(nil), stdout.Original...)
	f.output = &mvpNativeExecutionOutput{owner: f, value: engine.TaskOutputObservation{NativePostgreSQLRestoreCompletion: &stdout, Bytes: len(stdout.Original)}}
	return plan, scopeID, request, connection, archive, task, id, policy, f
}

func TestMVPNativeRestoreOriginalExecutionAttachesBeforeOneStartAndRetainsBothOriginals(t *testing.T) {
	// This is a named SDK-method double, not an actual SQL/native process run.
	plan, scopeID, r, connection, archive, task, id, policy, f := mvpNativeExecutionFixture(t)
	out, complete, e := executeMVPNativeRestoreOriginalTask(context.Background(), f, f.attach, policy, plan, scopeID, r, connection, archive, task, id, nil)
	want := []string{"inspect", "attach", "start", "inspect", "wait", "inspect", "complete", "inspect", "close"}
	if e != nil || complete == nil || out.NativePostgreSQLRestoreCompletion == nil || f.starts != 1 || f.inspector.reads != 1 || !reflect.DeepEqual(f.events, want) {
		t.Fatal("one original command/output order failed", e, f.events)
	}
	f.output.value.NativePostgreSQLRestoreCompletion.Original[0] = '!'
	f.inspector.output.NativePostgreSQLRestoreCompletion.Original[0] = '?'
	if bytes.HasPrefix(out.NativePostgreSQLRestoreCompletion.Original, []byte("!")) || bytes.HasPrefix(complete.Original, []byte("?")) {
		t.Fatal("private stdout/file originals alias caller memory")
	}
}

func TestMVPNativeRestoreOriginalExecutionKeepsStdoutAndFileRawDigestsSeparate(t *testing.T) {
	plan, scopeID, r, connection, archive, task, id, policy, f := mvpNativeExecutionFixture(t)
	stdout := f.output.value.NativePostgreSQLRestoreCompletion
	stdout.Original = append([]byte(" \n"), stdout.Original...)
	stdout.OriginalSHA256 = wire.Digest(stdout.Original)
	f.output.value.Bytes = len(stdout.Original)
	out, complete, e := executeMVPNativeRestoreOriginalTask(context.Background(), f, f.attach, policy, plan, scopeID, r, connection, archive, task, id, nil)
	if e != nil || complete == nil || out.NativePostgreSQLRestoreCompletion == nil || complete.OriginalSHA256 == out.NativePostgreSQLRestoreCompletion.OriginalSHA256 || !bytes.Equal(stdout.Original, out.NativePostgreSQLRestoreCompletion.Original) {
		t.Fatal("original stdout impersonated the completion file or was normalized", e)
	}
}

func TestMVPNativeRestoreOriginalExecutionNeverReplaysUnknownStartOrReplacesActor(t *testing.T) {
	for _, name := range []string{"attach-error", "start-unknown", "replacement", "missing", "oom", "exit-failure", "paused", "canceled-running"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, r, connection, archive, task, id, policy, f := mvpNativeExecutionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "attach-error":
				f.attachError = errors.New("synthetic attach failure")
			case "start-unknown":
				f.startError = errors.New("synthetic lost start acknowledgement")
				f.started = func(f *mvpNativeExecutionDouble) {
					f.inspector.actor.State.Status = "running"
					f.inspector.actor.State.Running = true
				}
			case "replacement":
				f.started = func(f *mvpNativeExecutionDouble) { f.inspector.actor.ID = strings.Repeat("e", 64) }
			case "missing":
				f.started = func(f *mvpNativeExecutionDouble) { f.inspector.found = false }
			case "oom":
				f.started = func(f *mvpNativeExecutionDouble) { f.inspector.actor.State.OOMKilled = true }
			case "exit-failure":
				f.started = func(f *mvpNativeExecutionDouble) { f.inspector.actor.State.ExitCode = 1 }
			case "paused":
				f.started = func(f *mvpNativeExecutionDouble) { f.inspector.actor.State.Paused = true }
			case "canceled-running":
				f.started = func(f *mvpNativeExecutionDouble) {
					f.inspector.actor.State.Status = "running"
					f.inspector.actor.State.Running = true
					cancel()
				}
			}
			_, complete, e := executeMVPNativeRestoreOriginalTask(ctx, f, f.attach, policy, plan, scopeID, r, connection, archive, task, id, nil)
			wantStarts := 1
			if name == "attach-error" {
				wantStarts = 0
			}
			if e == nil || complete != nil || f.starts != wantStarts || f.output.waits != 0 || f.inspector.reads != 0 {
				t.Fatal("unknown/failed actor replayed or qualified", e)
			}
			if name == "start-unknown" {
				if _, _, e = executeMVPNativeRestoreOriginalTask(context.Background(), f, f.attach, policy, plan, scopeID, r, connection, archive, task, id, nil); e == nil || f.starts != 1 || f.attaches != 1 {
					t.Fatal("second invocation retried an already started original")
				}
			}
		})
	}
}

func TestMVPNativeRestoreOriginalExecutionRefusesUnadmittedOrPreviouslyStartedInputs(t *testing.T) {
	for _, name := range []string{"missing-pins", "wrong-request", "wrong-source", "foreign-id", "missing-actor", "exited", "started-time", "invalid-policy", "wrong-kind", "nil-attach", "canceled"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, r, connection, archive, task, id, policy, f := mvpNativeExecutionFixture(t)
			ctx := context.Background()
			var attach mvpNativeRestoreAttach = f.attach
			switch name {
			case "missing-pins":
				r.TargetRoots = nil
				task.NativePostgreSQLRecovery.RestoreRequest = &r
			case "wrong-request":
				r.Policy.OperationSeconds++
			case "wrong-source":
				archive = strings.Repeat("0", 64)
				plan.Source.SourceScopeID = scopeID
			case "foreign-id":
				id = strings.Repeat("e", 64)
			case "missing-actor":
				f.inspector.found = false
			case "exited":
				f.inspector.actor.State.Status = "exited"
			case "started-time":
				f.inspector.actor.State.StartedAt = "2026-11-03T00:00:00Z"
			case "invalid-policy":
				policy.Process.ReservePercent = 1
			case "wrong-kind":
				task.Kind = engine.NativePostgreSQLRestoreVerify
				task.Owner.Role = "database-" + task.Kind
			case "nil-attach":
				attach = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, _, e := executeMVPNativeRestoreOriginalTask(ctx, f, attach, policy, plan, scopeID, r, connection, archive, task, id, nil); e == nil || f.starts != 0 || f.attaches != 0 {
				t.Fatal("unadmitted/old actor started")
			}
		})
	}
}

func TestMVPNativeRestoreOriginalExecutionRequiresExactStructuredStdoutAndCompletion(t *testing.T) {
	for _, name := range []string{"wait-error", "stdout-missing", "stdout-redacted", "stdout-code", "stdout-context", "file-missing", "different-originals"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, r, connection, archive, task, id, policy, f := mvpNativeExecutionFixture(t)
			switch name {
			case "wait-error":
				f.output.err = errors.New("synthetic original output lost")
			case "stdout-missing":
				f.output.value.NativePostgreSQLRestoreCompletion = nil
			case "stdout-redacted":
				f.output.value.Redacted = true
			case "stdout-code":
				f.output.value.StdoutCode = "SQL_COMPLETE"
			case "stdout-context":
				c := f.output.value.NativePostgreSQLRestoreCompletion
				c.RuntimeContext.TargetDatabase = "different_database"
				c.Original = encode(c)
				c.OriginalSHA256 = wire.Digest(c.Original)
				f.output.value.Bytes = len(c.Original)
			case "file-missing":
				f.inspector.output.NativePostgreSQLRestoreCompletion = nil
			case "different-originals":
				c := f.inspector.output.NativePostgreSQLRestoreCompletion
				c.Qualification = "Different actual file qualification"
				c.Original = encode(c)
				c.OriginalSHA256 = wire.Digest(c.Original)
			}
			if _, _, e := executeMVPNativeRestoreOriginalTask(context.Background(), f, f.attach, policy, plan, scopeID, r, connection, archive, task, id, nil); e == nil || f.starts != 1 || f.output.waits != 1 {
				t.Fatal("missing/changed originals qualified", e)
			}
		})
	}
}
