package scope

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit process-metadata double. A running process is never represented as
// evidence of SQL readiness; these tests exercise admission and payload only.
type liveDatabaseAdmissionDouble struct {
	*replacementEngineDouble
	process func(*engine.RuntimeContainer)
}

func (d *liveDatabaseAdmissionDouble) InspectRuntime(ctx context.Context, scope, name string) (engine.RuntimeContainer, bool, error) {
	r, found, e := d.replacementEngineDouble.InspectRuntime(ctx, scope, name)
	if name == "postgres" && found && e == nil {
		r.State.Running = r.State.Status == "running"
		if r.State.Running {
			r.State.PID = 123
		}
		if d.process != nil {
			d.process(&r)
		}
	}
	return r, found, e
}

func TestRestoredLiveDatabaseAdmissionUsesCurrentTLSAndEffectivePeersWithoutEffects(t *testing.T) {
	c, _, original, w := completedDependencyFixture(t)
	if _, e := startDependencyDouble(t, c, original, w); e != nil {
		t.Fatal(e)
	}
	d := &liveDatabaseAdmissionDouble{replacementEngineDouble: original}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before := encode(l.events)
	counts := []int{d.creates, d.utilityCreates, d.puts, d.utilityRemoves, d.volumeRemoves, d.removed, w.databaseStarts, w.creates}
	plan, files, e := l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32))
	if e != nil {
		t.Fatal(e)
	}
	if plan.ScopeID != l.marker.ScopeID || plan.PostgresPID != 123 || plan.PostgresStartedAt == "" || plan.PayloadSHA256 != restoreConfigurationDigest(files) || len(plan.Tasks) != 2 || len(plan.Files) != len(files) {
		t.Fatal("invalid sanitized plan", plan)
	}
	for i, kind := range []string{engine.DatabaseSchemaCheck, engine.DatabaseRuntimeCheck} {
		task := plan.Tasks[i]
		if task.Kind != kind || task.ValidateRestoredDatabaseTask() != nil || task.Peers["control"] != d.newID || task.Peers["postgres"] != w.state.Services["postgres"].ID || task.Owner.OperationID != plan.GenerationID || task.VolumeOwner.OperationID != plan.GenerationID || task.Reason != "" || task.SourceOwner != nil || task.OutputOwner != nil || task.StoreRestore != nil {
			t.Fatal("broadened/stale task", task)
		}
	}
	tls, current, e := l.currentControlReplacementFiles(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{tls.CertificateName, tls.PrivateKeyName} {
		fresh, e1 := restoreNamedFile(current, name)
		merged, e2 := restoreNamedFile(files, name)
		if e1 != nil || e2 != nil || !bytes.Equal(fresh, merged) {
			t.Fatal("current TLS lost")
		}
		if bytes.Contains(encode(plan), fresh) {
			t.Fatal("private payload leaked")
		}
	}
	for i, file := range files {
		if plan.Files[i].Name != file.Name || plan.Files[i].Bytes != int64(len(file.Contents)) || plan.Files[i].SHA256 != wire.Digest(file.Contents) {
			t.Fatal("file index mismatch")
		}
	}
	if !bytes.Equal(before, encode(l.events)) || !reflect.DeepEqual(counts, []int{d.creates, d.utilityCreates, d.puts, d.utilityRemoves, d.volumeRemoves, d.removed, w.databaseStarts, w.creates}) {
		t.Fatal("inspection changed state")
	}
}

func TestRestoredLiveDatabaseAdmissionRejectsProcessAndCurrentPKIDrift(t *testing.T) {
	c, request, original, w := completedDependencyFixture(t)
	d := &liveDatabaseAdmissionDouble{replacementEngineDouble: original}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("stopped dependency admitted")
	}
	l.close()
	if _, e = startDependencyDouble(t, c, original, w); e != nil {
		t.Fatal(e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before := encode(l.events)
	for _, fault := range []string{"not-running", "pid", "started-at", "exit", "OOM", "paused", "dead", "restarting", "wrong-id"} {
		t.Run(fault, func(t *testing.T) {
			d.process = func(r *engine.RuntimeContainer) {
				switch fault {
				case "not-running":
					r.State.Running = false
				case "pid":
					r.State.PID = 0
				case "started-at":
					r.State.StartedAt = "invalid"
				case "exit":
					r.State.ExitCode = 1
				case "OOM":
					r.State.OOMKilled = true
				case "paused":
					r.State.Paused = true
				case "dead":
					r.State.Dead = true
				case "restarting":
					r.State.Restarting = true
				case "wrong-id":
					r.ID = strings.Repeat("8", 64)
				}
			}
			if _, _, e := l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
				t.Fatal("unsafe process admitted")
			}
		})
	}
	d.process = nil
	for _, id := range []string{"", l.events[0].OperationID, tlsProjectionState(l.events).record.GenerationID} {
		if _, _, e = l.makeRestoredLiveDatabasePlan(context.Background(), d, w, id); e == nil {
			t.Fatal("invalid/reused generation admitted")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, e = l.makeRestoredLiveDatabasePlan(cancelled, d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("cancelled inspection admitted")
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	if _, _, e = l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32)); e == nil {
		t.Fatal("missing current key admitted")
	}
	if !bytes.Equal(before, encode(l.events)) {
		t.Fatal("refused inspection changed journal")
	}
}
