package scope

import (
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The fixed task contains only connection identities and artifact facts. Its
// protected password payload is never serialized. Retaining these facts makes
// cleanup independent of an unavailable original backup after an interruption.
type RestoreSQLRecord struct {
	Native       *MVPNativeRestoreStageRecord `json:"native,omitempty"`
	PlanSHA256   string                       `json:"plan_sha256"`
	Initializer  string                       `json:"initializer"`
	Task         engine.PostgresBackupTask    `json:"task"`
	Step         int                          `json:"step"`
	Phase        string                       `json:"phase"`
	Role         string                       `json:"role,omitempty"`
	ObjectID     string                       `json:"object_id,omitempty"`
	ResultSHA256 string                       `json:"result_sha256,omitempty"`
}

type restoreSQLStep struct{ phase, role string }

func restoreSQLSteps() []restoreSQLStep {
	steps := []restoreSQLStep{}
	for _, role := range []string{"input", "source", "result"} {
		for _, phase := range []string{"volume-create", "utility-create", "payload-write", "utility-remove"} {
			steps = append(steps, restoreSQLStep{phase, role})
		}
	}
	for _, phase := range []string{"task-create", "task-execute", "result-verify", "task-remove"} {
		steps = append(steps, restoreSQLStep{phase, "postgres"})
	}
	for _, role := range []string{"input", "source", "result"} {
		steps = append(steps, restoreSQLStep{"volume-remove", role})
	}
	return append(steps, restoreSQLStep{"complete", ""})
}

type restoreSQLRecords struct {
	record                                   *RestoreSQLRecord
	pending, action, outcome, abortReference string
	completed                                map[int]RestoreSQLRecord
	failed, aborting, aborted, complete      bool
}

func restoreSQLState(events []Event) restoreSQLRecords {
	s := restoreSQLRecords{completed: map[int]RestoreSQLRecord{}}
	for _, e := range events {
		if e.RestoreSQL == nil {
			continue
		}
		r := *e.RestoreSQL
		s.record = &r
		s.outcome = e.Outcome
		if e.Action == "restore_sql_abort" {
			s.action = e.Action
			if e.Outcome == "started" {
				s.pending = e.OperationID
				s.aborting = true
				s.abortReference = e.ReferenceOperationID
			}
			if e.Outcome == "completed" {
				s.pending = ""
				s.aborting = false
				s.aborted = true
			}
			continue
		}
		if e.Action == "restore_sql_effect" {
			s.action = e.Action
		}
		if e.Outcome == "started" {
			s.pending = e.OperationID
		}
		if e.Outcome == "completed" {
			s.pending = ""
			s.completed[r.Step] = r
			s.complete = r.Phase == "complete"
		}
		if e.Outcome == "failed" {
			s.pending = ""
			s.failed = true
		}
	}
	return s
}

func restoreSQLAction(action string) bool {
	return action == "restore_sql_effect" || action == "restore_sql_reconcile" || action == "restore_sql_abort"
}

func restoreSQLIdentity(a, b RestoreSQLRecord) bool {
	a.Step, b.Step = 0, 0
	a.Phase, b.Phase = "", ""
	a.Role, b.Role = "", ""
	a.ObjectID, b.ObjectID = "", ""
	a.ResultSHA256, b.ResultSHA256 = "", ""
	return reflect.DeepEqual(a, b)
}

func validRestoreSQLRecord(r *RestoreSQLRecord, reservation *RestoreRecord, scopeID string) bool {
	if r == nil || reservation == nil || r.PlanSHA256 != reservation.PlanSHA256 || !immutableDigest(r.Initializer) || r.Step < 0 || r.Step >= len(restoreSQLStepsForRecord(r)) {
		return false
	}
	step := restoreSQLStepsForRecord(r)[r.Step]
	if step.phase != r.Phase || step.role != r.Role {
		return false
	}
	if r.Native != nil {
		return validMVPNativeRestoreStageRecord(r.Native, mvpNativeRestoreSQLStage, reservation.AttemptID) && reflect.DeepEqual(r.Task, engine.PostgresBackupTask{})
	}
	t := r.Task
	if t.Restore == nil || !immutableDigest(t.Image) || t.Owner.ScopeID != scopeID || t.Owner.OperationID != reservation.AttemptID || t.Owner.Role != "database-"+engine.PostgresRestoreKind || !utilityIDPattern.MatchString(t.NetworkID) || t.UID < 1 || t.GID < 1 || t.Host != "postgres" || t.Port < 1 || t.Port > 65535 || !restoreSQLName.MatchString(t.Database) || !restoreSQLName.MatchString(t.User) || len(t.Peers) < 2 || len(t.Peers) > 3 || t.Peers["postgres"] == "" || t.Peers["control"] == "" {
		return false
	}
	for role, id := range t.Peers {
		if (role != "postgres" && role != "control" && role != "gateway") || !utilityIDPattern.MatchString(id) {
			return false
		}
	}
	wanted := []struct {
		owner engine.Ownership
		role  string
	}{{t.InputOwner, engine.PostgresRestoreInputRole}, {t.OutputOwner, engine.PostgresRestoreOutputRole}, {t.Restore.SourceOwner, engine.PostgresRestoreSourceRole}}
	for _, v := range wanted {
		if v.owner.ScopeID != scopeID || v.owner.OperationID != reservation.AttemptID || v.owner.Role != v.role+"-"+reservation.AttemptID {
			return false
		}
	}
	p := t.Restore
	if p.SourceUID != t.UID || p.SourceGID != t.GID || !wire.ValidSHA256(p.ArchiveSHA256) || p.ArchiveBytes < engine.MinimumPostgresRestoreArchiveBytes || !p.ExpectedSource.Valid() || p.ExpectedSource.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion || len(p.ExpectedFiles) != 4 {
		return false
	}
	seen := map[string]bool{}
	names := map[string]bool{"database.dump": true, "contents.list": true, "source.json": true, "tools.txt": true}
	var total int64
	for _, f := range p.ExpectedFiles {
		if !names[f.Name] || seen[f.Name] || f.Bytes < 1 || f.Bytes > p.ArchiveBytes || total > p.ArchiveBytes-f.Bytes || !wire.ValidSHA256(f.SHA256) {
			return false
		}
		seen[f.Name] = true
		total += f.Bytes
	}
	return true
}

func restoreSQLResultValid(r RestoreSQLRecord, events []Event) bool {
	if r.Native != nil {
		return nativeRestoreSQLResultValid(r, events)
	}
	if r.Phase == "utility-create" || r.Phase == "task-create" {
		return utilityIDPattern.MatchString(r.ObjectID) && r.ResultSHA256 == ""
	}
	if r.Phase == "result-verify" {
		return r.ObjectID == "" && wire.ValidSHA256(r.ResultSHA256)
	}
	if r.Phase == "complete" {
		proof := RestoreSQLRecord{}
		for _, completed := range restoreSQLState(events).completed {
			if completed.Phase == "result-verify" {
				proof = completed
			}
		}
		return r.ObjectID == "" && wire.ValidSHA256(r.ResultSHA256) && proof.Phase == "result-verify" && proof.ResultSHA256 == r.ResultSHA256
	}
	return r.ObjectID == "" && r.ResultSHA256 == ""
}

func restoreSQLProof(events []Event) (string, bool) {
	s := restoreSQLState(events)
	if !s.complete || s.record == nil || s.aborted || s.failed || s.pending != "" || len(s.completed) != len(restoreSQLStepsForRecord(s.record)) {
		return "", false
	}
	if s.record.Native != nil {
		records := make([]RestoreSQLRecord, len(s.completed))
		for i := range records {
			r, ok := s.completed[i]
			if !ok {
				return "", false
			}
			records[i] = r
		}
		return wire.Digest(encode(records)), true
	}
	return wire.Digest(encode(*s.record)), true
}

func validateRestoreSQLEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RESTORE_SQL_TRANSITION_INVALID") }
	reservation := restoreState(events)
	s := restoreSQLState(events)
	r := e.RestoreSQL
	if !identityPattern.MatchString(e.OperationID) || !reservation.reserved || reservation.pending != "" || reservation.aborted || !validRestoreSQLRecord(r, reservation.record, e.ScopeID) || e.Restore != nil || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.record != nil && (!restoreSQLIdentity(*s.record, *r) || s.aborted || s.complete && e.Action != "restore_sql_abort") {
		return bad()
	}
	if e.Action == "restore_sql_abort" {
		if s.record == nil || r.Step != s.record.Step || r.ObjectID != "" || r.ResultSHA256 != "" {
			return bad()
		}
		if e.Outcome == "started" {
			if s.aborting || e.ReferenceOperationID != s.pending || e.Code != "" {
				return bad()
			}
			for _, prior := range events {
				if prior.OperationID == e.OperationID {
					return bad()
				}
			}
			return nil
		}
		if !s.aborting || e.OperationID != s.pending || e.ReferenceOperationID != s.abortReference {
			return bad()
		}
		if e.Outcome == "completed" && e.Code == "" || e.Outcome == "uncertain" && codePattern.MatchString(e.Code) {
			return nil
		}
		return bad()
	}
	if s.aborting || s.failed {
		return bad()
	}
	if e.Action == "restore_sql_effect" && e.Outcome == "started" {
		if s.pending != "" || e.ReferenceOperationID != "" || e.Code != "" || r.ObjectID != "" || r.ResultSHA256 != "" || r.Step != len(s.completed) {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.record == nil || s.pending == "" || r.Step != s.record.Step {
		return bad()
	}
	if e.Action == "restore_sql_effect" {
		if s.action != "restore_sql_effect" || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action == "restore_sql_reconcile" {
		if e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
	} else {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" && restoreSQLResultValid(*r, events) {
		return nil
	}
	if r.ObjectID != "" || r.ResultSHA256 != "" || !codePattern.MatchString(e.Code) {
		return bad()
	}
	if e.Outcome == "failed" && r.Phase == "task-execute" || e.Outcome == "uncertain" && e.Action == "restore_sql_effect" || e.Outcome == "unresolved" && e.Action == "restore_sql_reconcile" {
		return nil
	}
	return bad()
}
