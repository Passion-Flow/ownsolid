package scope

import (
	"path/filepath"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type BackupRecord struct {
	RestoredAdmission *RestoredApplicationAdmission `json:"restored_admission,omitempty"`
	AttemptID         string                        `json:"attempt_id"`
	PlanSHA256        string                        `json:"plan_sha256"`
	RequestSHA256     string                        `json:"request_sha256"`
	DestinationRoot   string                        `json:"destination_root"`
	Step              int                           `json:"step"`
	TotalSteps        int                           `json:"total_steps"`
	Phase             string                        `json:"phase"`
	Role              string                        `json:"role,omitempty"`
	ObjectID          string                        `json:"object_id,omitempty"`
	ArtifactSHA256    string                        `json:"artifact_sha256,omitempty"`
	ArtifactBytes     int64                         `json:"artifact_bytes,omitempty"`
}
type backupRecords struct {
	record         *BackupRecord
	pending        string
	terminal       bool
	completed      map[int]BackupRecord
	aborting       bool
	aborted        bool
	abortReference string
}

func backupState(events []Event) backupRecords {
	s := backupRecords{completed: map[int]BackupRecord{}}
	for _, e := range events {
		if e.Backup == nil {
			continue
		}
		r := *e.Backup
		if s.record == nil || s.record.AttemptID != r.AttemptID {
			s = backupRecords{completed: map[int]BackupRecord{}}
		}
		s.record = &r
		if e.Action == "backup_abort" {
			if e.Outcome == "started" {
				s.pending = e.OperationID
				s.aborting = true
				s.abortReference = e.ReferenceOperationID
			}
			if e.Outcome == "completed" {
				s.pending = ""
				s.terminal = true
				s.aborted = true
				s.aborting = false
			}
			continue
		}
		if e.Outcome == "started" {
			s.pending = e.OperationID
		}
		if e.Outcome == "completed" {
			s.pending = ""
			s.completed[r.Step] = r
			s.terminal = r.Phase == "complete"
		}
	}
	return s
}
func backupRecordIdentity(a, b BackupRecord) bool {
	a.ObjectID, b.ObjectID = "", ""
	a.ArtifactSHA256, b.ArtifactSHA256 = "", ""
	a.ArtifactBytes, b.ArtifactBytes = 0, 0
	return reflect.DeepEqual(a, b)
}
func validBackupResult(r BackupRecord) bool {
	switch r.Phase {
	case "reserve", "volume-create", "payload-write", "utility-remove", "task-execute", "task-remove", "volume-remove":
		return r.ObjectID == "" && r.ArtifactSHA256 == "" && r.ArtifactBytes == 0
	case "utility-create", "task-create":
		return utilityIDPattern.MatchString(r.ObjectID) && r.ArtifactSHA256 == "" && r.ArtifactBytes == 0
	case "archive-publish", "manifest-publish", "complete":
		return r.ObjectID == "" && wire.ValidSHA256(r.ArtifactSHA256) && r.ArtifactBytes > 0
	}
	return false
}

func validateBackupEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_BACKUP_TRANSITION_INVALID") }
	r := e.Backup
	s := backupState(events)
	if r == nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil || !identityPattern.MatchString(r.AttemptID) || !wire.ValidSHA256(r.PlanSHA256) || !wire.ValidSHA256(r.RequestSHA256) || r.DestinationRoot == "" || r.Step < 0 || r.TotalSteps < 2 || r.TotalSteps > 128 || r.Step >= r.TotalSteps {
		return bad()
	}
	if !filepath.IsAbs(r.DestinationRoot) || filepath.Clean(r.DestinationRoot) != r.DestinationRoot || len(r.DestinationRoot) > 1024 || strings.ContainsAny(r.DestinationRoot, "\x00\r\n") {
		return bad()
	}
	if r.Phase == "reserve" || r.Phase == "manifest-publish" || r.Phase == "complete" {
		if r.Role != "" {
			return bad()
		}
	} else if !wire.SafePath(r.Role) || strings.Contains(r.Role, "/") || len(r.Role) > 63 {
		return bad()
	}
	if e.Action == "backup_abort" {
		if s.record == nil || s.terminal || !backupRecordIdentity(*s.record, *r) {
			return bad()
		}
		if e.Outcome == "started" {
			if s.aborting || e.ReferenceOperationID != s.pending || e.Code != "" || r.ObjectID != "" || r.ArtifactSHA256 != "" || r.ArtifactBytes != 0 {
				return bad()
			}
			return nil
		}
		if !s.aborting || e.OperationID != s.pending || e.ReferenceOperationID != s.abortReference || r.ObjectID != "" {
			return bad()
		}
		if e.Outcome == "completed" {
			if e.Code != "" || !wire.ValidSHA256(r.ArtifactSHA256) || r.ArtifactBytes < 1 {
				return bad()
			}
		} else if e.Outcome != "uncertain" || !codePattern.MatchString(e.Code) || r.ArtifactSHA256 != "" || r.ArtifactBytes != 0 {
			return bad()
		}
		return nil
	}
	if s.aborting {
		return bad()
	}
	if e.Action != "backup_effect" && e.Action != "backup_reconcile" {
		return bad()
	}
	if e.Outcome == "started" {
		if e.Action != "backup_effect" || e.ReferenceOperationID != "" || e.Code != "" || s.pending != "" || r.ObjectID != "" || r.ArtifactSHA256 != "" || r.ArtifactBytes != 0 {
			return bad()
		}
		if s.record == nil || s.terminal {
			if r.Step != 0 || r.Phase != "reserve" || r.Role != "" || s.record != nil && s.record.AttemptID == r.AttemptID {
				return bad()
			}
			if len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || effectiveRuntimeState(events).pending != "" || modelTaskActionState(events).pending != "" {
				return bad()
			}
			if r.RestoredAdmission != nil {
				if !validRestoredBackupAdmission(events, r.RestoredAdmission) {
					return bad()
				}
			} else {
				if restoreState(events).reserved || dependencyAdmission(events) != nil || !databaseCreationProcessReady(events) || !schemaQualified(events) || !runtimeAccessQualified(events) || !bootstrapQualified(events) || !databaseTaskActionState(events).removed {
					return bad()
				}
				for _, role := range orderedStoreRoles {
					if _, declared := volumeState(events).roles[role]; declared && !storeQualified(events, role) {
						return bad()
					}
				}
			}
		} else {
			previous := s.record
			if r.AttemptID != previous.AttemptID || r.PlanSHA256 != previous.PlanSHA256 || r.RequestSHA256 != previous.RequestSHA256 || r.DestinationRoot != previous.DestinationRoot || r.TotalSteps != previous.TotalSteps || r.Step != previous.Step+1 || r.Phase == "reserve" || !reflect.DeepEqual(r.RestoredAdmission, previous.RestoredAdmission) {
				return bad()
			}
		}
		if r.Phase == "complete" && r.Step != r.TotalSteps-1 || r.Phase != "complete" && r.Step == r.TotalSteps-1 {
			return bad()
		}
		switch r.Phase {
		case "reserve", "volume-create", "payload-write", "utility-remove", "task-execute", "task-remove", "volume-remove", "utility-create", "task-create", "archive-publish", "manifest-publish", "complete":
			return nil
		}
		return bad()
	}
	if s.record == nil || s.pending == "" || !backupRecordIdentity(*s.record, *r) {
		return bad()
	}
	if e.Action == "backup_effect" {
		if e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.ReferenceOperationID != s.pending || e.OperationID == s.pending {
		return bad()
	}
	if e.Outcome == "completed" {
		if e.Code != "" || !validBackupResult(*r) {
			return bad()
		}
	} else if e.Outcome != "uncertain" && e.Outcome != "failed" && e.Outcome != "unresolved" || !codePattern.MatchString(e.Code) || r.ObjectID != "" || r.ArtifactSHA256 != "" || r.ArtifactBytes != 0 {
		return bad()
	}
	return nil
}
