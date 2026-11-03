package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Unused interface methods intentionally remain nil: an unexpected effect
// panics instead of silently succeeding in this explicitly named test double.
type restoreSQLStepDouble struct {
	restoreSQLEngine
	starts, puts, reads int
	configurationStatus string
	task                engine.RuntimeContainer
	startError          error
}

func (d *restoreSQLStepDouble) InspectConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	d.reads++
	return engine.ConfigurationObservation{Status: d.configurationStatus}, nil
}
func (d *restoreSQLStepDouble) PutConfiguration(context.Context, engine.Ownership, string, int, int, []engine.ConfigurationFile) error {
	d.puts++
	return nil
}
func (d *restoreSQLStepDouble) InspectPostgresRestoreArchiveInput(context.Context, engine.PostgresBackupTask, string) (engine.PostgresBackupArchive, error) {
	d.reads++
	return engine.PostgresBackupArchive{}, nil
}
func (d *restoreSQLStepDouble) InspectPostgresBackupTask(context.Context, engine.PostgresBackupTask) (engine.RuntimeContainer, bool, error) {
	d.reads++
	return d.task, true, nil
}
func (d *restoreSQLStepDouble) StartPostgresBackupTask(context.Context, engine.PostgresBackupTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error) {
	d.starts++
	return engine.ResourceAdmission{}, d.startError
}

func TestRestoreSQLReconcileNeverSubmitsPayloadOrRetriesProcess(t *testing.T) {
	events, base, _ := restoreSQLJournalFixture(t)
	l := &locked{events: events, config: configuration.Config{Compose: &configuration.Compose{Process: &wire.ProcessSettings{ObserveSeconds: 1, PollMilliseconds: 50, ReservePercent: 30}}}}
	r := base
	r.Phase = "payload-write"
	r.Role = "result"
	d := &restoreSQLStepDouble{configurationStatus: "observed_empty_owned_volume"}
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, configuration.RestoreRequest{}, true); e == nil {
		t.Fatal("bare result volume accepted as private initialized output")
	}
	if d.puts != 0 {
		t.Fatal("reconciliation submitted configuration")
	}
	d.configurationStatus = engine.ConfigurationPayloadVerified
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, configuration.RestoreRequest{}, true); e != nil {
		t.Fatal(e)
	}
	r.Role = "source"
	request := configuration.RestoreRequest{RestoreSourceRequest: configuration.RestoreSourceRequest{BackupRoot: "/deliberately/unavailable/host/source"}}
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, true); e != nil {
		t.Fatal("copy reconciliation attempted to open/resubmit host source", e)
	}
	if d.puts != 0 {
		t.Fatal("copy/configuration replay")
	}
	created := base
	created.Phase = "task-create"
	created.Role = "postgres"
	created.Step = 12
	created.ObjectID = strings.Repeat("a", 64)
	l.events = append(l.events, Event{Action: "restore_sql_effect", Outcome: "completed", RestoreSQL: &created})
	r.Role = "postgres"
	r.Phase = "task-execute"
	r.Step = 13
	d.task.ID = created.ObjectID
	d.task.State.Status = "created"
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, true); e == nil {
		t.Fatal("never-started SQL treated as successful")
	}
	if d.starts != 0 {
		t.Fatal("unknown SQL outcome restarted")
	}
	d.task.State.Status = "exited"
	d.task.State.ExitCode = 0
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, true); e != nil {
		t.Fatal(e)
	}
	d.startError = errors.New("SYNTHETIC_SUCCESSFUL_START_RESPONSE_LOST")
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, false); e == nil || d.starts != 1 {
		t.Fatal("single initial start", e)
	}
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, true); e != nil || d.starts != 1 {
		t.Fatal("lost start outcome not independently observed", e)
	}
	d.task.State.OOMKilled = true
	if _, e := l.executeRestoreSQLStep(context.Background(), d, r, nil, request, true); e == nil || e.Error() != restoreSQLTaskFailed {
		t.Fatal("OOM task qualified", e)
	}
}
