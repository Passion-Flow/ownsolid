package scope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit two-service SDK/effect doubles; no PostgreSQL process is simulated as SQL readiness.
type databaseEngineDouble struct {
	*processEngineDouble
	times map[string]string
}

func (d *databaseEngineDouble) InspectRuntime(_ context.Context, _ string, name string) (engine.RuntimeContainer, bool, error) {
	s, found := d.worker.state.Services[name]
	var r engine.RuntimeContainer
	r.ID = s.ID
	r.State.Status = s.State
	r.State.ExitCode = s.ExitCode
	r.State.StartedAt = d.times[name]
	return r, found, nil
}

type databaseWorkerDouble struct {
	*processWorkerDouble
	databaseEngine     *databaseEngineDouble
	databaseStarts     int
	databaseStartEpoch int
	databaseFault      string
}

func (w *databaseWorkerDouble) CreateRuntime(_ context.Context, _ compose.RuntimeReader, _ compose.RenderContext, model compose.Model) (compose.RuntimeStatus, error) {
	w.state = createdRuntimeFixture()
	w.state.Services["postgres"] = compose.RuntimeServiceStatus{ID: strings.Repeat("c", 64), State: "created"}
	return w.state, nil
}
func (w *databaseWorkerDouble) StartDatabaseRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	w.databaseStarts++
	if w.databaseFault == "before" {
		return w.state, errors.New("EXPLICIT_TEST_DATABASE_START_UNKNOWN")
	}
	w.databaseStartEpoch++
	s := w.state.Services["postgres"]
	s.State = "running"
	s.ExitCode = 0
	if w.databaseFault == "exit" {
		s.State, s.ExitCode = "exited", 1
	}
	w.state.Services["postgres"] = s
	w.databaseEngine.times["postgres"] = fmt.Sprintf("2026-10-17T00:00:%02dZ", w.databaseStartEpoch)
	if w.databaseFault == "other-changed" {
		other := w.state.Services["control"]
		other.State = "running"
		w.state.Services["control"] = other
		w.databaseEngine.times["control"] = "2026-10-17T00:00:01Z"
	}
	if w.databaseFault == "after" {
		return w.state, errors.New("EXPLICIT_TEST_DATABASE_START_RESPONSE_LOST")
	}
	return w.state, nil
}
func databaseProcessFixture(t *testing.T) (configuration.Config, *databaseEngineDouble, *databaseWorkerDouble) {
	return databaseProcessFixtureWithInputs(t, nil)
}

func databaseProcessFixtureWithInputs(t *testing.T, configure func(*preparedInputs, *compose.Model)) (configuration.Config, *databaseEngineDouble, *databaseWorkerDouble) {
	t.Helper()
	c, l, p := preparedInputFixtureConfigured(t, func(c *configuration.Config) {
		c.Compose.Process = &wire.ProcessSettings{StopSeconds: 2, ObserveSeconds: 1, PollMilliseconds: 50, ReservePercent: 30}
		if configure != nil {
			c.MaximumJournalBytes = 2 << 20
		}
	})
	var model compose.Model
	json.Unmarshal(p.model, &model)
	service := model.Services["control"]
	service.MemoryBytes = 128 << 20
	service.MilliCPU = 300
	model.Services["postgres"] = service
	if configure != nil {
		configure(&p, &model)
	}
	p.model = encode(model)
	p.descriptor.ModelSHA256 = wire.Digest(p.model)
	p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
	p.record.ModelSHA256 = p.descriptor.ModelSHA256
	p.record.CatalogSHA256 = p.descriptor.CatalogSHA256
	p.record.DescriptorSHA256 = wire.Digest(encode(p.descriptor))
	if _, e := prepareInputs(context.Background(), l, p); e != nil {
		l.close()
		t.Fatal(e)
	}
	images := imageState(l.events)
	admitted := images.completed["image"]
	base := &imageDouble{observation: *images.engine, found: true}
	base.image.ID = admitted.ManifestDigest
	base.image.OS = "linux"
	base.image.Architecture = "amd64"
	base.image.RootFS.Type = "layers"
	base.image.RootFS.Layers = admitted.DiffIDs
	l.close()
	storage := &storageDouble{imageDouble: base}
	if _, e := initializeStorage(context.Background(), c, storage, false); e != nil {
		t.Fatal(e)
	}
	inner := &processWorkerDouble{runtimeWorkerDouble: &runtimeWorkerDouble{state: compose.RuntimeStatus{Status: "observed_absent_compose_resources", Services: map[string]compose.RuntimeServiceStatus{}}}}
	client := &databaseEngineDouble{processEngineDouble: &processEngineDouble{runtimeEngineDouble: &runtimeEngineDouble{storageDouble: storage}, worker: inner}, times: map[string]string{"control": "0001-01-01T00:00:00Z", "postgres": "0001-01-01T00:00:00Z"}}
	inner.engine = client.processEngineDouble
	worker := &databaseWorkerDouble{processWorkerDouble: inner, databaseEngine: client}
	if _, e := createRuntime(context.Background(), c, client, worker, false); e != nil {
		t.Fatal(e)
	}
	return c, client, worker
}
func TestScopedDatabaseDependencyStartCapturesAllAndStartsOnlyPostgres(t *testing.T) {
	c, d, w := databaseProcessFixture(t)
	ctx := context.Background()
	r, e := processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"})
	if e != nil || r.Pending || w.databaseStarts != 1 || w.starts != 0 || d.admissions != 1 || r.Resources.Services["control"].State != "created" || r.Resources.Services["postgres"].State != "running" {
		t.Fatal(r, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	record := processState(l.events).record
	if record.Admission.Requirement.MemoryBytes != 128<<20 || record.Admission.Requirement.MilliCPU != 300 || len(record.Before) != 2 || len(record.After) != 2 || record.SelectedServices[0] != "postgres" {
		l.close()
		t.Fatal("wrong selected admission or incomplete snapshots")
	}
	l.close()
	if _, e = processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"}); e != nil || w.databaseStarts != 1 || d.admissions != 1 {
		t.Fatal("database start replay", e)
	}
	if _, e = Read(ctx, c); e != nil {
		t.Fatal("source-bound selected journal", e)
	}
}
func TestScopedDatabaseDependencyUnknownOutcomeReconcilesWithoutResend(t *testing.T) {
	for _, fault := range []string{"before", "after", "other-changed"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := databaseProcessFixture(t)
			w.databaseFault = fault
			ctx := context.Background()
			r, e := processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"})
			if e == nil || !r.Pending || w.databaseStarts != 1 {
				t.Fatal(r, e)
			}
			if _, e = processRuntimeSelected(ctx, c, d, w, "running", false, []string{"postgres"}); e == nil || w.databaseStarts != 1 {
				t.Fatal("unknown database start resent")
			}
			r, e = processRuntime(ctx, c, d, w, "", true)
			if fault == "after" {
				if e != nil || r.Pending {
					t.Fatal(r, e)
				}
			} else if e == nil || !r.Pending {
				t.Fatal("ambiguous/nonselected outcome passed", r, e)
			}
			if w.databaseStarts != 1 || w.starts != 0 {
				t.Fatal("reconcile started service")
			}
		})
	}
}

// The two-service double owns per-service start times. The embedded single-service
// double cannot update this separate map when starting the application peer.
func (w *databaseWorkerDouble) StartRuntime(ctx context.Context, reader compose.RuntimeReader, scope compose.RenderContext, model compose.Model, before compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	previous := w.state.Services["control"]
	result, e := w.processWorkerDouble.StartRuntime(ctx, reader, scope, model, before)
	if current := result.Services["control"]; current.State != "created" && previous.State != "running" {
		w.databaseEngine.times["control"] = w.engine.startedAt
	}
	return result, e
}
