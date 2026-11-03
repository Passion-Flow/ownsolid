package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit SDK double; real create/reconcile is covered by the tagged signed CLI journey.
type modelTaskDouble struct {
	*storageDouble
	task        bool
	taskID      string
	fault       string
	taskCreates int
}

func (d *modelTaskDouble) CreateModelTask(_ context.Context, _ engine.ModelStoreTask) (engine.RuntimeContainer, error) {
	d.taskCreates++
	if d.fault == "before" {
		return engine.RuntimeContainer{}, errors.New("EXPLICIT_TEST_CREATE_OUTCOME_UNKNOWN")
	}
	d.task = true
	value := createdTaskDouble(d.taskID)
	if d.fault == "after" {
		return value, errors.New("EXPLICIT_TEST_CREATE_RESPONSE_LOST")
	}
	return value, nil
}
func createdTaskDouble(id string) engine.RuntimeContainer {
	r := engine.RuntimeContainer{ID: id}
	r.State.Status = "created"
	r.State.StartedAt = "0001-01-01T00:00:00Z"
	return r
}
func (d *modelTaskDouble) InspectModelTask(_ context.Context, _ engine.ModelStoreTask) (engine.RuntimeContainer, bool, error) {
	r := createdTaskDouble(d.taskID)
	if d.fault == "running" {
		r.State.Status = "running"
		r.State.Running = true
		r.State.PID = 123
		r.State.StartedAt = "2026-10-17T00:00:00Z"
	}
	if d.fault == "stale-created" {
		r.State.StartedAt = "2026-10-17T00:00:00Z"
	}
	return r, d.task, nil
}
func scopedModelFixture(t *testing.T) (configuration.Config, *modelTaskDouble) {
	t.Helper()
	c, l, p := preparedInputFixtureConfigured(t, func(c *configuration.Config) {
		c.Engine.Utility = &wire.UtilityResources{MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16}
		c.Compose.Process = &wire.ProcessSettings{StopSeconds: 2, ObserveSeconds: 1, PollMilliseconds: 50, ReservePercent: 30}
	})
	var model compose.Model
	json.Unmarshal(p.model, &model)
	service := model.Services["control"]
	service.Mounts = []compose.Mount{{Type: "volume", Source: "models", Target: "/var/lib/projectone/models", ReadOnly: false}}
	model.Services["control"] = service
	model.Volumes = []string{"models"}
	p.model = encode(model)
	p.catalog = encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "image", Roles: []SourceRole{{Key: "models"}}})
	p.descriptor.ModelSHA256 = wire.Digest(p.model)
	p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
	empty := []InputFile{}
	p.descriptor.Roles = []InputRole{{Key: "models", UID: 10033, GID: 10034, Files: empty, PayloadSHA256: wire.Digest(encode(empty))}}
	p.record.DescriptorSHA256 = wire.Digest(encode(p.descriptor))
	p.record.ModelSHA256 = p.descriptor.ModelSHA256
	p.record.CatalogSHA256 = p.descriptor.CatalogSHA256
	p.files = map[string][]engine.ConfigurationFile{"models": {}}
	if _, e := prepareInputs(context.Background(), l, p); e != nil {
		l.close()
		t.Fatal(e)
	}
	state := imageState(l.events)
	admitted := state.completed["image"]
	d := &imageDouble{observation: *state.engine, found: true}
	d.image.ID = admitted.ManifestDigest
	d.image.OS = "linux"
	d.image.Architecture = "amd64"
	d.image.RootFS.Type = "layers"
	d.image.RootFS.Layers = admitted.DiffIDs
	l.close()
	storage := &storageDouble{imageDouble: d}
	if _, e := initializeStorage(context.Background(), c, storage, false); e != nil {
		t.Fatal(e)
	}
	return c, &modelTaskDouble{storageDouble: storage, taskID: strings.Repeat("e", 64)}
}
func TestScopedModelTaskCreationBindsSourcesAndPreservesCompletedRepeat(t *testing.T) {
	c, d := scopedModelFixture(t)
	ctx := context.Background()
	result, e := createModelTask(ctx, c, d, false)
	if e != nil || result.Pending || result.ID != d.taskID || d.taskCreates != 1 {
		t.Fatal(result, e)
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	result, e = createModelTask(ctx, c, d, false)
	if e != nil || result.ID != d.taskID || d.taskCreates != 1 {
		t.Fatal(result, e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("completed repeat changed journal")
	}
	s, e := Read(ctx, c)
	if e != nil || s.ModelTaskPending || s.ModelTaskID != d.taskID || s.Status != "isolated_scope_never_started_model_task_recorded" {
		t.Fatal(s, e)
	}
	d.taskID = strings.Repeat("f", 64)
	if _, e = createModelTask(ctx, c, d, false); e == nil {
		t.Fatal("changed recorded id accepted")
	}
}
func TestScopedModelTaskUnknownCreationNeverResends(t *testing.T) {
	for _, fault := range []string{"before", "after"} {
		t.Run(fault, func(t *testing.T) {
			c, d := scopedModelFixture(t)
			d.fault = fault
			ctx := context.Background()
			result, e := createModelTask(ctx, c, d, false)
			if e == nil || !result.Pending || d.taskCreates != 1 {
				t.Fatal(result, e)
			}
			if _, e = createModelTask(ctx, c, d, false); e == nil || d.taskCreates != 1 {
				t.Fatal("unknown creation replay")
			}
			result, e = createModelTask(ctx, c, d, true)
			if fault == "before" {
				if e == nil || !result.Pending {
					t.Fatal("absence resolved creation", result, e)
				}
			} else if e != nil || result.Pending || result.ID != d.taskID {
				t.Fatal(result, e)
			}
			if d.taskCreates != 1 {
				t.Fatal("reconcile created task")
			}
			if fault == "before" {
				l, e := open(c, true)
				if e != nil {
					t.Fatal(e)
				}
				if e = l.appendEvent(Event{Action: "engine_bind", OperationID: strings.Repeat("f", 32), Outcome: "completed", Engine: &d.observation}); e == nil {
					l.close()
					t.Fatal("pending task admitted unrelated effect")
				}
				l.close()
				d.task = true
				d.fault = "running"
				if _, e = createModelTask(ctx, c, d, true); e == nil {
					t.Fatal("running task resolved never-started creation")
				}
				d.fault = "stale-created"
				if _, e = createModelTask(ctx, c, d, true); e == nil {
					t.Fatal("stale start time resolved creation")
				}
				d.fault = ""
				result, e = createModelTask(ctx, c, d, true)
				if e != nil || result.Pending {
					t.Fatal(result, e)
				}
			}
		})
	}
}
func TestScopedModelTaskRefusesSourceDriftCancellationAndLockBeforeEffect(t *testing.T) {
	for _, fault := range []string{"engine", "rootfs", "volume", "input", "cancel", "lock", "capacity"} {
		t.Run(fault, func(t *testing.T) {
			c, d := scopedModelFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "engine":
				d.observation.EngineSHA256 = wire.Digest([]byte("foreign engine"))
			case "rootfs":
				d.image.RootFS.Layers = nil
			case "volume":
				d.volume = false
			case "input":
				os.WriteFile(filepath.Join(c.ScopeRoot, "inputs/descriptor.json"), []byte("{}"), 0600)
			case "cancel":
				cancel()
			case "lock":
				l, e := open(c, true)
				if e != nil {
					t.Fatal(e)
				}
				defer l.close()
			case "capacity":
				c.MaximumJournalBytes = 1
			}
			if _, e := createModelTask(ctx, c, d, false); e == nil || d.taskCreates != 0 {
				t.Fatal("unsafe creation effect", e)
			}
		})
	}
}
func TestScopedModelTaskForgedAndMixedTransitionRefusal(t *testing.T) {
	c, d := scopedModelFixture(t)
	d.fault = "after"
	createModelTask(context.Background(), c, d, false)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	events := l.events
	started := events[len(events)-2]
	terminal := events[len(events)-1]
	prefix := events[:len(events)-1]
	for _, change := range []func(*Event){func(e *Event) { e.ModelTask.VolumeSHA256 = wire.Digest([]byte("foreign")) }, func(e *Event) { e.ModelTask.Task.VolumeOwner.Role = "operator-config" }, func(e *Event) { e.ModelTask.Task.UID++ }, func(e *Event) { e.ModelTask.ID = "bad" }, func(e *Event) { e.Engine = &d.observation }, func(e *Event) { e.ReferenceOperationID = started.OperationID }, func(e *Event) { e.Outcome = "completed"; e.Code = "BAD" }} {
		copy := terminal
		record := *terminal.ModelTask
		copy.ModelTask = &record
		change(&copy)
		if validateModelTaskEvent(prefix, copy) == nil {
			t.Fatal("forged transition accepted")
		}
	}
	genesis := append([]Event(nil), events[:3]...)
	record := *terminal.ModelTask
	genesis[0].ModelTask = &record
	var raw []byte
	previous := ""
	for _, event := range genesis {
		event.PreviousSHA256 = previous
		line := encode(event)
		previous = wire.Digest(bytes.TrimSuffix(line, []byte("\n")))
		raw = append(raw, line...)
	}
	if _, _, e = validateJournal(raw, l.marker); e == nil {
		t.Fatal("task payload accepted in genesis")
	}
}
