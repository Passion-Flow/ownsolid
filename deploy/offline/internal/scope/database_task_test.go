package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit SDK/worker doubles; actual network/SQL uses the signed integration.
type scopedDatabaseTaskDouble struct {
	*databaseEngineDouble
	exists  bool
	creates int
	fault   string
	id      string
}

func (d *scopedDatabaseTaskDouble) CreateDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, error) {
	d.creates++
	if d.fault == "before" {
		return engine.RuntimeContainer{}, errors.New("TEST_DATABASE_CREATE_UNKNOWN")
	}
	d.exists = true
	r := createdTaskDouble(d.id)
	if d.fault == "after" {
		return r, errors.New("TEST_DATABASE_CREATE_LOST")
	}
	return r, nil
}
func (d *scopedDatabaseTaskDouble) InspectDatabaseTask(_ context.Context, task engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	r := createdTaskDouble(d.id)
	r.Name = "/projectone-" + task.Owner.ScopeID + "-database-" + task.Kind + "-" + task.Owner.OperationID
	if d.fault == "running" {
		r.State.Status = "running"
		r.State.Running = true
		r.State.PID = 42
		r.State.StartedAt = "2026-10-17T00:00:00Z"
	}
	if d.fault == "stale" {
		r.State.StartedAt = "2026-10-17T00:00:00Z"
	}
	return r, d.exists, nil
}
func (d *scopedDatabaseTaskDouble) InspectDatabaseTaskInput(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	if d.fault == "input" {
		return engine.ConfigurationObservation{}, errors.New("TEST_PRIVATE_INPUT_CHANGED")
	}
	return engine.ConfigurationObservation{Status: "verified_private_volume_payload"}, nil
}
func scopedDatabaseTaskFixture(t *testing.T, withBootstrap ...bool) (configuration.Config, *scopedDatabaseTaskDouble, *databaseWorkerDouble) {
	t.Helper()
	c, l, p := preparedInputFixtureConfigured(t, func(c *configuration.Config) {
		if len(withBootstrap) > 1 && withBootstrap[1] {
			c.MaximumJournalBytes = 1 << 20
		}
		c.Engine.Utility = &wire.UtilityResources{MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16}
		c.Compose.Process = &wire.ProcessSettings{StopSeconds: 2, ObserveSeconds: 1, PollMilliseconds: 50, ReservePercent: 30}
	})
	var model compose.Model
	json.Unmarshal(p.model, &model)
	service := model.Services["control"]
	service.Mounts = nil
	service.Restart = "no"
	model.Services["control"] = service
	model.Services["postgres"] = service
	model.Volumes = []string{"operator-config"}
	p.model = encode(model)
	data := []byte("explicit synthetic operator configuration")
	p.catalog = encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "image", Roles: []SourceRole{{Key: "operator-config", Files: []SourceFile{{Name: "control.yaml", SourceFile: "/explicit/synthetic/operator"}, {Name: "runtime.yaml", SourceFile: "/explicit/synthetic/runtime"}}}}})
	files := []InputFile{{Name: "control.yaml", Bytes: int64(len(data)), SHA256: wire.Digest(data)}, {Name: "runtime.yaml", Bytes: int64(len(data)), SHA256: wire.Digest(data)}}
	p.descriptor.ModelSHA256 = wire.Digest(p.model)
	p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
	p.descriptor.Roles = []InputRole{{Key: "operator-config", UID: 10033, GID: 10034, Files: files, PayloadSHA256: wire.Digest(encode(files))}}
	if len(withBootstrap) > 0 && withBootstrap[0] {
		var catalog SourceCatalog
		json.Unmarshal(p.catalog, &catalog)
		catalog.Roles[0].Files = append([]SourceFile{{Name: "bootstrap-request.json", SourceFile: "/explicit/synthetic/bootstrap"}}, catalog.Roles[0].Files...)
		catalog.Roles = append([]SourceRole{{Key: engine.BootstrapStateRole}}, catalog.Roles...)
		model.Volumes = append([]string{engine.BootstrapStateRole}, model.Volumes...)
		p.model = encode(model)
		p.catalog = encode(catalog)
		p.descriptor.ModelSHA256 = wire.Digest(p.model)
		p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
		files = append([]InputFile{{Name: "bootstrap-request.json", Bytes: int64(len(data)), SHA256: wire.Digest(data)}}, files...)
		p.descriptor.Roles[0].Files = files
		p.descriptor.Roles[0].PayloadSHA256 = wire.Digest(encode(files))
		p.descriptor.Roles = append([]InputRole{{Key: engine.BootstrapStateRole, UID: 10033, GID: 10034, Files: []InputFile{}, PayloadSHA256: wire.Digest(encode([]InputFile{}))}}, p.descriptor.Roles...)
	}
	var operationData []byte
	if len(withBootstrap) > 1 && withBootstrap[1] {
		operationData = encode(StoreRequest{FormatVersion: 1, Reason: "Synthetic scoped namespace initialization"})
		var catalog SourceCatalog
		json.Unmarshal(p.catalog, &catalog)
		for i := range catalog.Roles {
			if catalog.Roles[i].Key == "operator-config" {
				catalog.Roles[i].Files = append(catalog.Roles[i].Files, SourceFile{Name: "store-request.json", SourceFile: "/explicit/synthetic/store-operation"})
			}
		}
		control := model.Services["control"]
		for _, role := range orderedStoreRoles {
			model.Volumes = append(model.Volumes, role)
			control.Mounts = append(control.Mounts, compose.Mount{Type: "volume", Source: role, Target: "/var/lib/projectone/" + role})
			catalog.Roles = append(catalog.Roles, SourceRole{Key: role})
			p.descriptor.Roles = append(p.descriptor.Roles, InputRole{Key: role, UID: 10033, GID: 10034, Files: []InputFile{}, PayloadSHA256: wire.Digest(encode([]InputFile{}))})
		}
		model.Services["control"] = control
		for i := range p.descriptor.Roles {
			if p.descriptor.Roles[i].Key == "operator-config" {
				p.descriptor.Roles[i].Files = append(p.descriptor.Roles[i].Files, InputFile{Name: "store-request.json", Bytes: int64(len(operationData)), SHA256: wire.Digest(operationData)})
				p.descriptor.Roles[i].PayloadSHA256 = wire.Digest(encode(p.descriptor.Roles[i].Files))
			}
		}
		sort.Slice(catalog.Roles, func(i, j int) bool { return catalog.Roles[i].Key < catalog.Roles[j].Key })
		sort.Slice(p.descriptor.Roles, func(i, j int) bool { return p.descriptor.Roles[i].Key < p.descriptor.Roles[j].Key })
		p.model = encode(model)
		p.catalog = encode(catalog)
		p.descriptor.ModelSHA256 = wire.Digest(p.model)
		p.descriptor.CatalogSHA256 = wire.Digest(p.catalog)
	}
	p.record.DescriptorSHA256 = wire.Digest(encode(p.descriptor))
	p.record.ModelSHA256 = p.descriptor.ModelSHA256
	p.record.CatalogSHA256 = p.descriptor.CatalogSHA256
	p.files = map[string][]engine.ConfigurationFile{"operator-config": {{Name: "control.yaml", Contents: data}, {Name: "runtime.yaml", Contents: data}}}
	if len(withBootstrap) > 0 && withBootstrap[0] {
		p.files["operator-config"] = append([]engine.ConfigurationFile{{Name: "bootstrap-request.json", Contents: data}}, p.files["operator-config"]...)
		p.files[engine.BootstrapStateRole] = []engine.ConfigurationFile{}
	}
	if len(operationData) > 0 {
		p.files["operator-config"] = append(p.files["operator-config"], engine.ConfigurationFile{Name: "store-request.json", Contents: operationData})
		for _, role := range orderedStoreRoles {
			p.files[role] = []engine.ConfigurationFile{}
		}
	}
	if _, e := prepareInputs(context.Background(), l, p); e != nil {
		l.close()
		t.Fatal(e)
	}
	s := imageState(l.events)
	admitted := s.completed["image"]
	base := &imageDouble{observation: *s.engine, found: true}
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
	w := &databaseWorkerDouble{processWorkerDouble: &processWorkerDouble{runtimeWorkerDouble: &runtimeWorkerDouble{state: compose.RuntimeStatus{Status: "observed_absent_compose_resources", Services: map[string]compose.RuntimeServiceStatus{}}}}}
	db := &databaseEngineDouble{processEngineDouble: &processEngineDouble{runtimeEngineDouble: &runtimeEngineDouble{storageDouble: storage}, worker: w.processWorkerDouble}, times: map[string]string{"control": "0001-01-01T00:00:00Z", "postgres": "0001-01-01T00:00:00Z"}}
	w.engine = db.processEngineDouble
	w.databaseEngine = db
	if _, e := createRuntime(context.Background(), c, db, w, false); e != nil {
		t.Fatal(e)
	}
	if _, e := processRuntimeSelected(context.Background(), c, db, w, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	return c, &scopedDatabaseTaskDouble{databaseEngineDouble: db, id: strings.Repeat("e", 64)}, w
}
func TestScopedDatabaseCreationRepeatAndStatus(t *testing.T) {
	c, d, w := scopedDatabaseTaskFixture(t)
	ctx := context.Background()
	r, e := createDatabaseMigrationTask(ctx, c, d, w, false)
	if e != nil || r.Pending || d.creates != 1 {
		t.Fatal(r, e, d.creates)
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if _, e = createDatabaseMigrationTask(ctx, c, d, w, false); e != nil || d.creates != 1 {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("completed create changed journal")
	}
	s, e := Read(ctx, c)
	if e != nil || s.DatabaseTaskID != d.id || s.DatabaseTaskPending || s.Status != "isolated_scope_never_started_database_task_recorded" {
		t.Fatal(s, e)
	}
	d.id = strings.Repeat("f", 64)
	if _, e = createDatabaseMigrationTask(ctx, c, d, w, false); e == nil || d.creates != 1 {
		t.Fatal("changed ID adopted")
	}
}
func TestScopedDatabaseUnknownCreateAndReadonlyReconcile(t *testing.T) {
	for _, fault := range []string{"before", "after"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := scopedDatabaseTaskFixture(t)
			d.fault = fault
			ctx := context.Background()
			r, e := createDatabaseMigrationTask(ctx, c, d, w, false)
			if e == nil || !r.Pending || d.creates != 1 {
				t.Fatal(r, e)
			}
			if _, e = createDatabaseMigrationTask(ctx, c, d, w, false); e == nil || d.creates != 1 {
				t.Fatal("unknown create resent")
			}
			r, e = createDatabaseMigrationTask(ctx, c, d, w, true)
			if fault == "before" {
				if e == nil || !r.Pending {
					t.Fatal("absence marked complete")
				}
				d.exists = true
				r, e = createDatabaseMigrationTask(ctx, c, d, w, true)
			}
			if e != nil || r.Pending || r.ID != d.id || d.creates != 1 {
				t.Fatal(r, e)
			}
		})
	}
}
func TestScopedDatabaseChangedInputStateAndPendingGuards(t *testing.T) {
	for _, fault := range []string{"running", "stale", "input"} {
		t.Run(fault, func(t *testing.T) {
			c, d, w := scopedDatabaseTaskFixture(t)
			d.fault = "after"
			if _, e := createDatabaseMigrationTask(context.Background(), c, d, w, false); e == nil {
				t.Fatal("expected uncertainty")
			}
			d.fault = fault
			if _, e := createDatabaseMigrationTask(context.Background(), c, d, w, true); e == nil || d.creates != 1 {
				t.Fatal("changed task reconciled")
			}
		})
	}
	c, d, w := scopedDatabaseTaskFixture(t)
	d.fault = "before"
	createDatabaseMigrationTask(context.Background(), c, d, w, false)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	if e = l.appendEvent(Event{OperationID: strings.Repeat("a", 32), Action: "process_stop", Outcome: "started"}); e == nil {
		t.Fatal("pending database allowed other effect")
	}
	s := databaseTaskState(l.events)
	if s.record == nil {
		t.Fatal("missing intent")
	}
	original := *s.record
	for _, change := range []func(*DatabaseTaskRecord){func(r *DatabaseTaskRecord) { r.RuntimeSHA256 = wire.Digest([]byte("foreign")) }, func(r *DatabaseTaskRecord) { r.Task.VolumeOwner.Role = "control-config" }, func(r *DatabaseTaskRecord) { r.Task.UID++ }, func(r *DatabaseTaskRecord) { r.Task.NetworkID = strings.Repeat("f", 64) }, func(r *DatabaseTaskRecord) { r.Task.Kind = engine.DatabaseSchemaCheck }, func(r *DatabaseTaskRecord) { r.ID = "bad" }} {
		raw := encode(original)
		var copy DatabaseTaskRecord
		json.Unmarshal(raw, &copy)
		change(&copy)
		e := Event{OperationID: strings.Repeat("9", 32), Action: "database_task_reconcile", ReferenceOperationID: s.pending, Outcome: "completed", DatabaseTask: &copy}
		if e2 := l.appendEvent(e); e2 == nil {
			t.Fatal("forged binding accepted")
		}
	}
	r := original
	r.ID = d.id
	if e = l.appendEvent(Event{OperationID: strings.Repeat("9", 32), Action: "database_task_reconcile", ReferenceOperationID: s.pending, Outcome: "completed", DatabaseTask: &r, Process: &ProcessRecord{}}); e == nil {
		t.Fatal("mixed journal fields accepted")
	}
	if !reflect.DeepEqual(original, *s.record) {
		t.Fatal("test mutated original intent")
	}
}

type databaseViewDouble struct {
	runtimeEngine
	network engine.RuntimeNetwork
	objects []engine.RuntimeObject
	allowed []string
}

func (d *databaseViewDouble) InspectRuntimeNetwork(context.Context, string) (engine.RuntimeNetwork, bool, error) {
	return d.network, true, nil
}
func (d *databaseViewDouble) RuntimeCandidates(context.Context, string, bool) ([]engine.RuntimeObject, error) {
	return d.objects, nil
}
func (d *databaseViewDouble) VerifyVolumeConsumers(_ context.Context, _ engine.Ownership, allowed []string) error {
	d.allowed = append([]string{}, allowed...)
	return nil
}
func TestDatabaseTaskReadonlyViewExcludesOnlyValidatedExactID(t *testing.T) {
	id, foreign := strings.Repeat("a", 64), strings.Repeat("b", 64)
	scopeID := strings.Repeat("c", 32)
	owner := engine.Ownership{ScopeID: scopeID, OperationID: strings.Repeat("d", 32), Role: "operator-config"}
	d := &databaseViewDouble{network: engine.RuntimeNetwork{ID: strings.Repeat("e", 64), Containers: map[string]struct{ Name string }{id: {"exact-task"}, foreign: {"foreign"}}}, objects: []engine.RuntimeObject{{ID: id}, {ID: foreign, Labels: map[string]string{"io.projectone.isolated-scope": scopeID}}}}
	v := databaseTaskRuntimeView{runtimeEngine: d, task: engine.DatabaseTask{Owner: engine.Ownership{ScopeID: scopeID}, VolumeOwner: owner, NetworkID: d.network.ID}, object: engine.RuntimeContainer{ID: id, Name: "/exact-task"}}
	n, found, e := v.InspectRuntimeNetwork(context.Background(), scopeID)
	if e != nil || !found || len(n.Containers) != 1 || n.Containers[foreign].Name != "foreign" || len(d.network.Containers) != 2 {
		t.Fatal("network filter changed other endpoints", e)
	}
	objects, e := v.RuntimeCandidates(context.Background(), scopeID, false)
	if e != nil || len(objects) != 1 || objects[0].ID != foreign || len(d.objects) != 2 {
		t.Fatal("label-only filter", e)
	}
	objects, e = v.RuntimeCandidates(context.Background(), scopeID, true)
	if e != nil || len(objects) != 2 {
		t.Fatal("network inventory filtered", e)
	}
	allowed := make([]string, 1, 3)
	allowed[0] = foreign
	if e = v.VerifyVolumeConsumers(context.Background(), owner, allowed); e != nil || !reflect.DeepEqual(d.allowed, []string{foreign, id}) || len(allowed) != 1 {
		t.Fatal("operator volume allowance", e)
	}
	other := owner
	other.Role = "control-config"
	v.VerifyVolumeConsumers(context.Background(), other, allowed)
	if !reflect.DeepEqual(d.allowed, allowed) {
		t.Fatal("other volume allowance weakened")
	}
	d.network.Containers[id] = struct{ Name string }{"changed"}
	if _, _, e = v.InspectRuntimeNetwork(context.Background(), scopeID); e == nil {
		t.Fatal("changed endpoint filtered")
	}
	if _, e = v.RuntimeCandidates(context.Background(), strings.Repeat("f", 32), false); e == nil {
		t.Fatal("cross-scope filtering accepted")
	}
	d.objects = append(d.objects, engine.RuntimeObject{ID: id})
	if _, e = v.RuntimeCandidates(context.Background(), scopeID, false); e == nil {
		t.Fatal("duplicate candidates hidden")
	}
}
