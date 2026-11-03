package scope

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func restoreSQLSnapshotFixture(t *testing.T, ports ...int) (*locked, RestorePlan, RestoreSQLRecord) {
	l, p, r, _, _ := restoreSQLSnapshotRuntimeFixture(t, ports...)
	return l, p, r
}

func restoreSQLSnapshotRuntimeFixture(t *testing.T, ports ...int) (*locked, RestorePlan, RestoreSQLRecord, *databaseEngineDouble, *databaseWorkerDouble) {
	return restoreSQLSnapshotRuntimeWithStoresFixture(t, nil, ports...)
}
func restoreSQLSnapshotRuntimeWithStoresFixture(t *testing.T, stores []string, ports ...int) (*locked, RestorePlan, RestoreSQLRecord, *databaseEngineDouble, *databaseWorkerDouble) {
	return restoreSQLSnapshotRuntimeWithStoresAndInputFixture(t, stores, nil, ports...)
}
func restoreSQLSnapshotRuntimeWithStoresAndInputFixture(t *testing.T, stores []string, mutate func(*preparedInputs), ports ...int) (*locked, RestorePlan, RestoreSQLRecord, *databaseEngineDouble, *databaseWorkerDouble) {
	return restoreSQLSnapshotRuntimeWithConfiguredInputsFixture(t, stores, mutate, nil, ports...)
}
func restoreSQLSnapshotRuntimeWithConfiguredInputsFixture(t *testing.T, stores []string, mutate func(*preparedInputs), configureModel func(*compose.Model), ports ...int) (*locked, RestorePlan, RestoreSQLRecord, *databaseEngineDouble, *databaseWorkerDouble) {
	t.Helper()
	selectedPort := postgresInitialPort
	if len(ports) > 0 {
		selectedPort = ports[0]
	}
	c, d, w := databaseProcessFixtureWithInputs(t, func(p *preparedInputs, model *compose.Model) {
		input, declared, _, _ := restoreTargetInputFixture()
		p.files = input.files
		for _, role := range stores {
			p.files[role] = []engine.ConfigurationFile{}
		}
		if selectedPort != postgresInitialPort {
			for role, files := range p.files {
				for i, file := range files {
					if strings.HasSuffix(file.Name, "-dsn") {
						p.files[role][i].Contents = bytes.ReplaceAll(file.Contents, []byte(":"+strconv.Itoa(postgresInitialPort)+"/"), []byte(":"+strconv.Itoa(selectedPort)+"/"))
					}
				}
			}
		}
		p.files["postgres"] = []engine.ConfigurationFile{}
		if mutate != nil {
			mutate(p)
		}
		for _, role := range []string{"control", "postgres"} {
			service := model.Services[role]
			service.Mounts = declared.Services[role].Mounts
			if role == "control" {
				for _, store := range stores {
					service.Mounts = append(service.Mounts, compose.Mount{Type: "volume", Source: store, Target: "/var/lib/projectone/" + store})
				}
			}
			service.Environment = declared.Services[role].Environment
			if role == "postgres" {
				service.User = "999:999"
				service.Mounts = append(service.Mounts, compose.Mount{Type: "volume", Source: "postgres", Target: "/var/lib/postgresql"})
				if selectedPort != postgresInitialPort {
					service.Command = []string{"postgres", "-p", strconv.Itoa(selectedPort)}
				}
			}
			model.Services[role] = service
		}
		if configureModel != nil {
			configureModel(model)
		}
		keys := []string{}
		for role := range p.files {
			keys = append(keys, role)
		}
		sort.Strings(keys)
		model.Volumes = keys
		catalog := SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "image", Roles: []SourceRole{}}
		p.descriptor.Roles = []InputRole{}
		for _, role := range keys {
			sort.Slice(p.files[role], func(i, j int) bool { return p.files[role][i].Name < p.files[role][j].Name })
			input := InputRole{Key: role, UID: 10033, GID: 10034, Files: []InputFile{}}
			if role == "postgres" || role == "postgres-config" {
				input.UID = 999
				input.GID = 999
			}
			catalogRole := SourceRole{Key: role}
			for _, file := range p.files[role] {
				input.Files = append(input.Files, InputFile{Name: file.Name, Bytes: int64(len(file.Contents)), SHA256: wire.Digest(file.Contents)})
				catalogRole.Files = append(catalogRole.Files, SourceFile{Name: file.Name, SourceFile: path.Join("/explicit/synthetic", role, file.Name)})
			}
			input.PayloadSHA256 = wire.Digest(encode(input.Files))
			p.descriptor.Roles = append(p.descriptor.Roles, input)
			catalog.Roles = append(catalog.Roles, catalogRole)
		}
		p.catalog = encode(catalog)
	})
	if _, e := processRuntimeSelected(context.Background(), c, d, w, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	r, source, target := restoreReservationInput(l)
	if len(stores) > 0 {
		r.BackupRoot = t.TempDir()
		if e := os.Chmod(r.BackupRoot, 0700); e != nil {
			t.Fatal(e)
		}
		source.RequestSHA256 = wire.Digest(encode(r.RestoreSourceRequest))
		source.Stores = append([]string{}, stores...)
		sort.Strings(source.Stores)
		target.Stores = append([]string{}, source.Stores...)
		target.BindingSHA256 = ""
		target.BindingSHA256 = wire.Digest(encode(target))
	}
	if _, e = l.reserveRestore(context.Background(), r, source, target, "prepare-restore", func(p RestorePlan) error { return writeBackupFile(l.root, restorePlanFile, encode(p)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	raw, e := regular(l.root, restorePlanFile, c.Verification.MaximumMetadataBytes)
	var plan RestorePlan
	if e != nil || wire.StrictJSON(raw, &plan) != nil {
		l.close()
		t.Fatal("reserved real private snapshot", e)
	}
	reservation := restoreState(l.events).record
	runtime := runtimeState(l.events).record
	input, _, _ := inputsState(l.events)
	owner := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: role + "-" + plan.AttemptID}
	}
	peers := map[string]string{}
	for role, s := range runtime.Services {
		peers[role] = s.ID
	}
	task := engine.PostgresBackupTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: "database-" + engine.PostgresRestoreKind}, InputOwner: owner(engine.PostgresRestoreInputRole), OutputOwner: owner(engine.PostgresRestoreOutputRole), Image: input.ImageID, UID: 999, GID: 999, NetworkID: runtime.NetworkID, Peers: peers, Host: "postgres", Port: selectedPort, Database: "synthetic_restore", User: "synthetic_operator", Restore: &engine.PostgresRestoreProfile{SourceOwner: owner(engine.PostgresRestoreSourceRole), SourceUID: 999, SourceGID: 999, ArchiveSHA256: strings.Repeat("c", 64), ArchiveBytes: 20000, ExpectedSource: source.Source, ExpectedFiles: []engine.BackupArchiveFile{}}}
	for _, name := range []string{"database.dump", "contents.list", "source.json", "tools.txt"} {
		task.Restore.ExpectedFiles = append(task.Restore.ExpectedFiles, engine.BackupArchiveFile{Name: name, Bytes: 10, SHA256: strings.Repeat("d", 64)})
	}
	record := RestoreSQLRecord{PlanSHA256: reservation.PlanSHA256, Initializer: input.ImageID, Task: task, Phase: "volume-create", Role: "input"}
	return l, plan, record, d, w
}

func TestRecordedRestoreSQLBindsRealImmutableSnapshotAndRejectsMismatches(t *testing.T) {
	l, plan, record := restoreSQLSnapshotFixture(t)
	defer l.close()
	// This verifies real snapshot/configuration/journal files with explicitly
	// synthetic task metadata and Engine doubles. No PostgreSQL is executed.
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &record}); e != nil {
		t.Fatal(e)
	}
	if e = l.verifyRecordedRestoreSQL(plan); e != nil {
		t.Fatal("positive immutable target context", e)
	}
	if files, e := l.restoreSQLPassword(context.Background(), record.Task); e != nil || len(files) != 1 || files[0].Name != engine.PostgresBackupPasswordName {
		t.Fatal("private target password reference", e)
	}
	for _, fault := range []string{"database", "operator", "port", "image", "initializer", "network", "source-tenant", "archive-budget", "numeric-user", "peer"} {
		t.Run(fault, func(t *testing.T) {
			copy := record
			task := copy.Task
			restore := *task.Restore
			task.Restore = &restore
			copy.Task = task
			copy.Task.Peers = maps.Clone(task.Peers)
			differentPlan := plan
			switch fault {
			case "database":
				copy.Task.Database = "different_target"
			case "operator":
				copy.Task.User = "different_operator"
			case "port":
				copy.Task.Port = 5544
			case "image":
				copy.Task.Image = "sha256:" + strings.Repeat("f", 64)
			case "initializer":
				copy.Initializer = "sha256:" + strings.Repeat("f", 64)
			case "network":
				copy.Task.NetworkID = strings.Repeat("f", 64)
			case "source-tenant":
				copy.Task.Restore.ExpectedSource.TenantID = "22222222-2222-4222-8222-222222222222"
			case "archive-budget":
				differentPlan.Request.MaximumBytes = 1024
			case "numeric-user":
				copy.Task.UID = 1000
				copy.Task.Restore.SourceUID = 1000
			case "peer":
				copy.Task.Peers["control"] = strings.Repeat("f", 64)
			}
			original := l.events[len(l.events)-1].RestoreSQL
			l.events[len(l.events)-1].RestoreSQL = &copy
			if e := l.verifyRecordedRestoreSQL(differentPlan); e == nil {
				t.Fatal("wrong recorded restore context accepted")
			}
			l.events[len(l.events)-1].RestoreSQL = original
		})
	}
}

func TestRecordedRestoreSQLUsesNondefaultPrivatePortWithoutRebuild(t *testing.T) {
	l, plan, record := restoreSQLSnapshotFixture(t, 5544)
	defer l.close()
	id, e := nonce()
	if e != nil {
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "restore_sql_effect", Outcome: "started", RestoreSQL: &record}); e != nil {
		t.Fatal(e)
	}
	if e = l.verifyRecordedRestoreSQL(plan); e != nil {
		t.Fatal("nondefault snapshot connection", e)
	}
	files, e := l.restoreSQLPassword(context.Background(), record.Task)
	if e != nil || len(files) != 1 || !bytes.Contains(files[0].Contents, []byte("postgres:5544:")) {
		t.Fatal("nondefault fixed client connection", e)
	}
}
