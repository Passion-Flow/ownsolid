package scope

import (
	"context"
	"reflect"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoredApplicationAdmission struct {
	Dependency          RestoredDependencyAdmission `json:"dependency"`
	DatabaseProofSHA256 string                      `json:"database_proof_sha256"`
	DatabasePlanSHA256  string                      `json:"database_plan_sha256"`
	StoresProofSHA256   string                      `json:"stores_proof_sha256"`
	StoresPlanSHA256    string                      `json:"stores_plan_sha256"`
	PayloadSHA256       string                      `json:"payload_sha256"`
	ContextSHA256       string                      `json:"context_sha256"`
	PostgresPID         int                         `json:"postgres_pid"`
	PostgresStartedAt   string                      `json:"postgres_started_at"`
}

type currentControlConfigurationReader interface {
	InspectCurrentControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
}

type failedControlConfigurationReader interface {
	InspectFailedControlConfiguration(context.Context, engine.Ownership, string, string, int, int, []engine.ConfigurationFile) (engine.ConfigurationObservation, error)
}
type restoredApplicationEngine interface {
	controlReplacementEngine
	currentControlConfigurationReader
}
type controlProcessOperator interface {
	StartControlRuntime(context.Context, compose.RuntimeReader, compose.RenderContext, compose.Model, compose.RuntimeStatus) (compose.RuntimeStatus, error)
}

func applicationAdmissionFromPlan(p RestoredCurrentStoresPlan, proof, plan string) RestoredApplicationAdmission {
	return RestoredApplicationAdmission{Dependency: p.Admission, DatabaseProofSHA256: p.DatabaseProofSHA256, DatabasePlanSHA256: p.DatabasePlanSHA256, StoresProofSHA256: proof, StoresPlanSHA256: plan, PayloadSHA256: p.PayloadSHA256, ContextSHA256: p.ContextSHA256, PostgresPID: p.PostgresPID, PostgresStartedAt: p.PostgresStartedAt}
}

// An observed process that started outside our admitted completed action cannot
// turn an old store check into activation authority.
func knownActivatedControl(events []Event, observed ProcessSnapshot) bool {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Process == nil || e.Process.RestoredApplication == nil || e.Outcome != "completed" || e.Process.Target != "running" {
			continue
		}
		actual := e.Process.After["control"]
		return actual.State == "running" && actual.ID == observed.ID && actual.StartedAt == observed.StartedAt
	}
	return false
}

func validApplicationProcess(events []Event, r ProcessRecord) bool {
	a := r.RestoredApplication
	dependency := dependencyAdmission(events)
	database, databaseOK := liveDatabaseProof(events)
	stores, storesOK := currentStoresProof(events)
	db, st := liveDatabaseState(events), currentStoresState(events)
	if a == nil || r.RestoredDependency != nil || dependency == nil || !databaseOK || !storesOK || db.record == nil || st.record == nil || r.Target != "running" || len(r.SelectedServices) != 1 || r.SelectedServices[0] != "control" || !reflect.DeepEqual(a.Dependency, *dependency) || a.DatabaseProofSHA256 != database || a.DatabasePlanSHA256 != db.record.PlanSHA256 || a.StoresProofSHA256 != stores || a.StoresPlanSHA256 != st.record.PlanSHA256 || st.record.DatabaseProofSHA256 != database || !reflect.DeepEqual(st.record.Admission, *dependency) || !wire.ValidSHA256(a.PayloadSHA256) || !wire.ValidSHA256(a.ContextSHA256) || a.PostgresPID <= 0 {
		return false
	}
	postgres, databaseFound := r.Before["postgres"]
	control, controlFound := r.Before["control"]
	retried := r.RetryOfOperationID != "" && validRecordedProcessRetry(events, r)
	started, parse := time.Parse(time.RFC3339Nano, a.PostgresStartedAt)
	if !databaseFound || postgres.State != "running" || postgres.ExitCode != 0 || postgres.StartedAt != a.PostgresStartedAt || parse != nil || started.IsZero() || !controlFound || control.ExitCode != 0 && !retried || r.RetryOfOperationID != "" && !retried {
		return false
	}
	return control.State == "created" || control.State == "exited" && retried || (control.State == "running" || control.State == "exited") && knownActivatedControl(events, control)
}

func StartRestoredApplication(ctx context.Context, c configuration.Config) (ProcessResult, error) {
	return startRestoredApplication(ctx, c, false)
}

func RetryRestoredApplication(ctx context.Context, c configuration.Config) (ProcessResult, error) {
	return startRestoredApplication(ctx, c, true)
}

func startRestoredApplication(ctx context.Context, c configuration.Config, retry bool) (ProcessResult, error) {
	if c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return ProcessResult{}, failure("OFFLINE_SCOPE_RESTORED_APPLICATION_CONFIGURATION_REQUIRED")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return ProcessResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return ProcessResult{}, e
	}
	l, e := open(c, true)
	if e != nil {
		return ProcessResult{}, e
	}
	defer l.close()
	return l.processSelectedAttemptLocked(ctx, client, worker, "running", false, []string{"control"}, restoredApplicationProcess, retry)
}

func (l *locked) admitRestoredApplication(ctx context.Context, c restoredApplicationEngine, w runtimeOperator) (*RestoredApplicationAdmission, error) {
	return l.admitRestoredApplicationAttempt(ctx, c, w, "")
}

func (l *locked) admitRestoredApplicationAttempt(ctx context.Context, c restoredApplicationEngine, w runtimeOperator, retryOf string) (*RestoredApplicationAdmission, error) {
	return l.admitRestoredApplicationRecovery(ctx, c, w, retryOf, nil)
}

func (l *locked) admitRestoredApplicationRecovery(ctx context.Context, c restoredApplicationEngine, w runtimeOperator, retryOf string, recovery *configuration.ProcessRecoveryRequest) (*RestoredApplicationAdmission, error) {
	if recovery != nil && recovery.Validate() != nil {
		return nil, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_REQUEST_INVALID")
	}
	databaseProof, databaseOK := liveDatabaseProof(l.events)
	storesProof, storesOK := currentStoresProof(l.events)
	s := currentStoresState(l.events)
	if !databaseOK || !storesOK || s.record == nil || processState(l.events).pending != "" {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_QUALIFICATION_REQUIRED")
	}
	p, e := l.readCurrentStoresPlan(*s.record)
	if e != nil {
		return nil, e
	}
	db := liveDatabaseState(l.events)
	if db.record == nil || p.DatabaseProofSHA256 != databaseProof || p.DatabasePlanSHA256 != db.record.PlanSHA256 {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_SQL_STORES_CHANGED")
	}
	// This validates current PKI, not just the historical certificate fingerprint.
	tls, files, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		return nil, e
	}
	verified, e := l.verifyRuntimeContext(ctx, c, w, false)
	if e != nil {
		return nil, e
	}
	if e = currentSelectedStoreProfile(files, l.marker.Profile); e != nil {
		return nil, e
	}
	postgres, found, e := c.InspectRuntime(ctx, l.marker.ScopeID, "postgres")
	if e != nil || !found || postgres.ID != verified.observed.Services["postgres"].ID || postgres.State.Status != "running" || !postgres.State.Running || postgres.State.PID != p.PostgresPID || postgres.State.StartedAt != p.PostgresStartedAt || postgres.State.ExitCode != 0 || postgres.State.OOMKilled || postgres.State.Paused || postgres.State.Restarting || postgres.State.Dead {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_DATABASE_PROCESS_CHANGED")
	}
	before := map[string]ProcessSnapshot{}
	for name, service := range verified.observed.Services {
		object, present, readError := c.InspectRuntime(ctx, l.marker.ScopeID, name)
		if readError != nil || !present || object.ID != service.ID || object.State.Status != service.State || object.State.ExitCode != service.ExitCode {
			return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_PEER_CHANGED")
		}
		before[name] = ProcessSnapshot{ID: service.ID, State: service.State, ExitCode: service.ExitCode, StartedAt: object.State.StartedAt}
		if name != "postgres" && name != "control" && (service.State != "created" && service.State != "exited" || service.ExitCode != 0) {
			return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_STOPPED_PEERS_REQUIRED")
		}
	}
	admission := applicationAdmissionFromPlan(p, storesProof, s.record.PlanSHA256)
	record := ProcessRecord{AttemptVersion: processAttemptVersion, RetryOfOperationID: retryOf, CreationSHA256: wire.Digest(encode(*verified.creation.record)), Target: "running", SelectedServices: []string{"control"}, Before: before, RestoredApplication: &admission}
	if activated, _ := activatedControlStart(l.events, record.CreationSHA256); retryOf != "" && retryOf == activated && recovery == nil {
		record.AttemptVersion = processLateFailureAttemptVersion
	}
	if recovery != nil {
		context, valid := stoppedRecoveryAtSnapshot(l.events, record.CreationSHA256, before, recovery.FailedControlStartOperationID, true)
		if !valid {
			return nil, failure("OFFLINE_SCOPE_PROCESS_RECOVERY_CONTEXT_CHANGED")
		}
		record.AttemptVersion, record.RecoveryOfOperationID, record.RecoveryStopOperationID, record.RecoveryReason = processRecoveryAttemptVersion, recovery.FailedControlStartOperationID, context.stopID, recovery.Reason
	}
	if retryOf != "" && !validProcessRetry(l.events, record) || !validApplicationProcess(l.events, record) {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_ADMISSION_CHANGED")
	}
	destination := ""
	for _, mount := range verified.model.Services["control"].Mounts {
		if mount.Source == tls.ConfigurationOwner.Role {
			if destination != "" || !mount.ReadOnly {
				return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_MOUNT_REQUIRED")
			}
			destination = mount.Target
		}
	}
	if destination == "" {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_MOUNT_REQUIRED")
	}
	read := c.InspectCurrentControlConfiguration
	if before["control"].ExitCode != 0 {
		failed, supported := c.(failedControlConfigurationReader)
		if retryOf == "" || !supported {
			return nil, failure("OFFLINE_SCOPE_FAILED_CONTROL_CONFIGURATION_READBACK_REQUIRED")
		}
		read = failed.InspectFailedControlConfiguration
	}
	observed, e := read(ctx, tls.ConfigurationOwner, before["control"].ID, destination, tls.UID, tls.GID, files)
	if e != nil {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_READBACK_REQUIRED:" + engine.DiagnosticCode(e))
	}
	if observed.Status != engine.ConfigurationPayloadVerified {
		return nil, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_READBACK_REQUIRED")
	}
	return &admission, nil
}

// Reopen historical activation facts at their original intent prefix; later
// SQL/store requalification must neither rewrite nor invalidate old history.
func (l *locked) verifyApplicationAdmissions() error {
	for i, e := range l.events {
		if e.Process == nil || e.Process.RestoredApplication == nil || e.Outcome != "started" || e.Action != "process_start" {
			continue
		}
		view := *l
		view.events = l.events[:i]
		stores := currentStoresState(view.events)
		if stores.record == nil {
			return failure("OFFLINE_SCOPE_RESTORED_APPLICATION_HISTORY_CHANGED")
		}
		p, readError := view.readCurrentStoresPlan(*stores.record)
		proof, ok := currentStoresProof(view.events)
		expected := applicationAdmissionFromPlan(p, proof, stores.record.PlanSHA256)
		if readError != nil || !ok || !reflect.DeepEqual(*e.Process.RestoredApplication, expected) {
			return failure("OFFLINE_SCOPE_RESTORED_APPLICATION_HISTORY_CHANGED")
		}
	}
	return nil
}

// Generated-actor filtering must preserve the real SDK's configuration reader.
// Forward only the fixed readonly method; no arbitrary effect is exposed here.
func readCurrentControlConfiguration(ctx context.Context, client any, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	reader, ok := client.(currentControlConfigurationReader)
	if !ok {
		return engine.ConfigurationObservation{}, failure("OFFLINE_SCOPE_CURRENT_APPLICATION_CONFIGURATION_READBACK_REQUIRED")
	}
	return reader.InspectCurrentControlConfiguration(ctx, owner, id, destination, uid, gid, files)
}

func (v liveDatabaseRuntimeEngine) InspectCurrentControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	return readCurrentControlConfiguration(ctx, v.liveDatabaseEngine, owner, id, destination, uid, gid, files)
}

func (v currentStoresRuntimeEngine) InspectCurrentControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	return readCurrentControlConfiguration(ctx, v.currentStoresEngine, owner, id, destination, uid, gid, files)
}

func readFailedControlConfiguration(ctx context.Context, client any, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	reader, ok := client.(failedControlConfigurationReader)
	if !ok {
		return engine.ConfigurationObservation{}, failure("OFFLINE_SCOPE_FAILED_CONTROL_CONFIGURATION_READBACK_REQUIRED")
	}
	return reader.InspectFailedControlConfiguration(ctx, owner, id, destination, uid, gid, files)
}

func (v liveDatabaseRuntimeEngine) InspectFailedControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	return readFailedControlConfiguration(ctx, v.liveDatabaseEngine, owner, id, destination, uid, gid, files)
}

func (v currentStoresRuntimeEngine) InspectFailedControlConfiguration(ctx context.Context, owner engine.Ownership, id, destination string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	return readFailedControlConfiguration(ctx, v.currentStoresEngine, owner, id, destination, uid, gid, files)
}
