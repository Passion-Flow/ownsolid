package scope

import (
	"reflect"
	"slices"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type DatabaseTaskRecord struct {
	PreviousTaskSHA256 string              `json:"previous_task_sha256,omitempty"`
	OutputVolumeSHA256 string              `json:"output_volume_sha256,omitempty"`
	SchemaSHA256       string              `json:"schema_sha256,omitempty"`
	AccessSHA256       string              `json:"access_sha256,omitempty"`
	MigrationSHA256    string              `json:"migration_sha256,omitempty"`
	DescriptorSHA256   string              `json:"descriptor_sha256"`
	VolumeSHA256       string              `json:"volume_sha256"`
	RuntimeSHA256      string              `json:"runtime_sha256"`
	Task               engine.DatabaseTask `json:"task"`
	ID                 string              `json:"id,omitempty"`
}
type databaseTaskRecords struct {
	record           *DatabaseTaskRecord
	pending, outcome string
}

// Bind the three independently recorded migration outcomes. Schema-check
// actions cannot contribute to this proof because their creation hash differs.
func migrationProof(events []Event) (string, bool) {
	return completedDatabaseTaskProof(events, engine.DatabaseMigrate)
}
func schemaProof(events []Event) (string, bool) {
	return completedDatabaseTaskProof(events, engine.DatabaseSchemaCheck)
}
func accessPreparationProof(events []Event) (string, bool) {
	return completedDatabaseTaskProof(events, engine.DatabaseRuntimePrepare)
}
func completedDatabaseTaskProof(events []Event, kind string) (string, bool) {
	return databaseTaskTerminalProof(events, kind, true)
}
func databaseTaskTerminalProof(events []Event, kind string, requireSuccess bool) (string, bool) {
	var creation *DatabaseTaskRecord
	execution, removal := "", ""
	for _, e := range events {
		if e.DatabaseTask != nil && e.DatabaseTask.Task.Kind == kind && e.Outcome == "completed" {
			creation = e.DatabaseTask
			execution, removal = "", ""
		}
		if creation == nil || !isDatabaseTaskAction(e.Action) || e.DatabaseTaskAction == nil || e.DatabaseTaskAction.CreationSHA256 != wire.Digest(encode(*creation)) {
			continue
		}
		r := e.DatabaseTaskAction
		if r.Target == "execute" && e.Outcome == "completed" {
			execution = wire.Digest(encode(*r))
		}
		if r.Target == "execute" && e.Outcome == "failed" {
			execution = ""
			if !requireSuccess {
				execution = wire.Digest(encode(*r))
			}
		}
		if r.Target == "removed" && e.Outcome == "completed" {
			removal = wire.Digest(encode(*r))
		}
	}
	if creation == nil || execution == "" || removal == "" {
		return "", false
	}
	return wire.Digest(encode(struct{ Creation, Execution, Removal string }{wire.Digest(encode(*creation)), execution, removal})), true
}
func schemaQualified(events []Event) bool {
	_, ok := schemaProof(events)
	return ok
}
func runtimeAccessQualified(events []Event) bool {
	_, ok := completedDatabaseTaskProof(events, engine.DatabaseRuntimeCheck)
	return ok && schemaQualified(events)
}
func nextDatabaseTaskKind(previous, next string) bool {
	return previous == engine.DatabaseMigrate && next == engine.DatabaseSchemaCheck || previous == engine.DatabaseSchemaCheck && next == engine.DatabaseRuntimePrepare || previous == engine.DatabaseRuntimePrepare && next == engine.DatabaseRuntimeCheck
}

func databaseCreationProcessReady(events []Event) bool {
	p := processState(events)
	return p.pending == "" && p.record != nil && p.outcome == "completed" && p.record.Target == "running" && slices.Equal(p.record.SelectedServices, []string{"postgres"})
}
func databaseTaskState(events []Event) databaseTaskRecords {
	s := databaseTaskRecords{}
	for _, e := range events {
		if e.Action == "database_task_create" {
			s.record = e.DatabaseTask
			s.outcome = e.Outcome
			s.pending = e.OperationID
			if e.Outcome == "completed" {
				s.pending = ""
			}
		}
		if e.Action == "database_task_reconcile" {
			s.record = e.DatabaseTask
			s.outcome = e.Outcome
			if e.Outcome == "completed" {
				s.pending = ""
			}
		}
	}
	return s
}
func databaseTaskIdentity(a, b DatabaseTaskRecord) bool { a.ID = b.ID; return reflect.DeepEqual(a, b) }
func validDatabaseTaskRecord(events []Event, r *DatabaseTaskRecord, scopeID string) bool {
	if r == nil || r.ID != "" && !utilityIDPattern.MatchString(r.ID) {
		return false
	}
	input, outcome, _ := inputsState(events)
	v, ok := volumeState(events).roles["operator-config"]
	runtime := runtimeState(events)
	if input == nil || outcome != "completed" || r.DescriptorSHA256 != input.DescriptorSHA256 || !ok || v.Phase != phasePrepared || wire.Digest(encode(v)) != r.VolumeSHA256 || !runtime.completed || runtime.record == nil || wire.Digest(encode(*runtime.record)) != r.RuntimeSHA256 {
		return false
	}
	t := r.Task
	if !storeKind(t.Kind) && t.Reason != "" {
		return false
	}
	if !bootstrapKind(t.Kind) && !storeKind(t.Kind) && (t.OutputOwner != nil || r.OutputVolumeSHA256 != "" || r.PreviousTaskSHA256 != "") {
		return false
	}
	if t.Kind == engine.DatabaseMigrate {
		if r.MigrationSHA256 != "" || r.SchemaSHA256 != "" || r.AccessSHA256 != "" {
			return false
		}
	} else if t.Kind == engine.DatabaseSchemaCheck {
		proof, ok := migrationProof(events)
		if !ok || r.MigrationSHA256 != proof || r.SchemaSHA256 != "" || r.AccessSHA256 != "" {
			return false
		}
	} else if t.Kind == engine.DatabaseRuntimePrepare || t.Kind == engine.DatabaseRuntimeCheck {
		proof, ok := schemaProof(events)
		if !ok || r.SchemaSHA256 != proof || r.MigrationSHA256 != "" {
			return false
		}
		if t.Kind == engine.DatabaseRuntimePrepare {
			if r.AccessSHA256 != "" {
				return false
			}
		} else {
			proof, ok = accessPreparationProof(events)
			if !ok || r.AccessSHA256 != proof {
				return false
			}
		}
	} else if bootstrapKind(t.Kind) {
		history := beforeDatabaseTaskCreation(events, t.Owner.OperationID)
		previous, ok := bootstrapPredecessorProof(history, t.Kind)
		schema, schemaOK := schemaProof(history)
		access, accessOK := completedDatabaseTaskProof(history, engine.DatabaseRuntimeCheck)
		output, exists := volumeState(events).roles[engine.BootstrapStateRole]
		if !ok || !schemaOK || !accessOK || r.PreviousTaskSHA256 != previous || r.SchemaSHA256 != schema || r.AccessSHA256 != access || r.MigrationSHA256 != "" || !exists || output.Phase != phasePrepared || r.OutputVolumeSHA256 != wire.Digest(encode(output)) || t.OutputOwner == nil || *t.OutputOwner != (engine.Ownership{ScopeID: scopeID, OperationID: output.ResourceOperationID, Role: engine.BootstrapStateRole}) || output.UID != t.UID || output.GID != t.GID || output.ImageID != t.Image {
			return false
		}
	} else if storeKind(t.Kind) {
		history := beforeDatabaseTaskCreation(events, t.Owner.OperationID)
		previous, ok := storePredecessorProof(history, t.Kind)
		role, mode, _ := engine.StoreTaskContext(t.Kind)
		schema, schemaOK := schemaProof(history)
		access, accessOK := completedDatabaseTaskProof(history, engine.DatabaseRuntimeCheck)
		output, exists := volumeState(events).roles[role]
		if !ok || !schemaOK || !accessOK || r.PreviousTaskSHA256 != previous || r.SchemaSHA256 != schema || r.AccessSHA256 != access || r.MigrationSHA256 != "" || !exists || output.Phase != phasePrepared || r.OutputVolumeSHA256 != wire.Digest(encode(output)) || t.OutputOwner == nil || *t.OutputOwner != (engine.Ownership{ScopeID: scopeID, OperationID: output.ResourceOperationID, Role: role}) || output.UID != t.UID || output.GID != t.GID || output.ImageID != t.Image || mode == "check" && t.Reason != "" || mode != "check" && !engine.ValidStoreTaskReason(t.Reason) {
			return false
		}
	} else {
		return false
	}
	if t.Owner.ScopeID != scopeID || t.Owner.Role != "database-"+t.Kind || !identityPattern.MatchString(t.Owner.OperationID) || t.VolumeOwner != (engine.Ownership{ScopeID: scopeID, OperationID: v.ResourceOperationID, Role: v.Key}) || t.Image != input.ImageID || t.Image != v.ImageID || t.UID != v.UID || t.GID != v.GID || t.NetworkID != runtime.record.NetworkID || len(t.Peers) != len(runtime.record.Services) || t.Peers["postgres"] == "" {
		return false
	}
	for role, s := range runtime.record.Services {
		if t.Peers[role] != s.ID {
			return false
		}
	}
	return true
}
func validateDatabaseTaskEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_DATABASE_TASK_TRANSITION_INVALID") }
	if e.DatabaseTaskAction != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || !validDatabaseTaskRecord(events, e.DatabaseTask, e.ScopeID) || !databaseCreationProcessReady(events) || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || runtimeState(events).pending != "" || modelTaskState(events).pending != "" || modelTaskActionState(events).pending != "" {
		return bad()
	}
	if m := modelTaskState(events); m.record != nil && (!modelTaskActionState(events).removed || !modelTaskActionState(events).initialized) {
		return bad()
	}
	s := databaseTaskState(events)
	if e.Action == "database_task_create" && e.Outcome == "started" {
		advancing := s.record != nil && (nextDatabaseTaskKind(s.record.Task.Kind, e.DatabaseTask.Task.Kind) || bootstrapTransition(events, e.DatabaseTask.Task.Kind) || storeTransition(events, e.DatabaseTask.Task.Kind)) && databaseTaskActionState(events).removed
		if s.record != nil && !advancing || e.ReferenceOperationID != "" || e.Code != "" || e.DatabaseTask.ID != "" || e.OperationID != e.DatabaseTask.Task.Owner.OperationID {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.pending == "" || s.record == nil || !databaseTaskIdentity(*s.record, *e.DatabaseTask) || s.record.ID != "" && s.record.ID != e.DatabaseTask.ID {
		return bad()
	}
	if e.Action == "database_task_create" {
		if s.pending != e.OperationID || s.outcome != "started" || e.ReferenceOperationID != "" {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" && utilityIDPattern.MatchString(e.DatabaseTask.ID) || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		return bad()
	}
	if e.Action == "database_task_reconcile" {
		if e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		if e.Outcome == "completed" && e.Code == "" && utilityIDPattern.MatchString(e.DatabaseTask.ID) || e.Outcome == "unresolved" && codePattern.MatchString(e.Code) {
			return nil
		}
	}
	return bad()
}
