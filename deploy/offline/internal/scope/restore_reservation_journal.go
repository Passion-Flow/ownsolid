package scope

import (
	"slices"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreRecord struct {
	AttemptID     string `json:"attempt_id"`
	PlanSHA256    string `json:"plan_sha256"`
	BindingSHA256 string `json:"binding_sha256"`
}

type restoreRecords struct {
	record                                   *RestoreRecord
	pending, action, outcome, abortReference string
	reserved, aborted                        bool
}

func restoreState(events []Event) restoreRecords {
	s := restoreRecords{}
	for _, e := range events {
		if e.Restore == nil {
			continue
		}
		r := *e.Restore
		s.record = &r
		s.outcome = e.Outcome
		switch e.Action {
		case "restore_reserve":
			s.action = e.Action
			if e.Outcome == "started" {
				s.pending = e.OperationID
			}
			if e.Outcome == "completed" {
				s.reserved = true
				s.pending = ""
			}
		case "restore_reconcile_reservation":
			if e.Outcome == "completed" {
				s.reserved = true
				s.pending = ""
			}
		case "restore_abort_reservation":
			s.action = e.Action
			if e.Outcome == "started" {
				s.pending = e.OperationID
				s.abortReference = e.ReferenceOperationID
			}
			if e.Outcome == "completed" {
				s.aborted = true
				s.pending = ""
			}
		}
	}
	return s
}

func restoreReservationAction(action string) bool {
	return action == "restore_reserve" || action == "restore_reconcile_reservation" || action == "restore_abort_reservation"
}

func validateRestoreReservationEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_RESTORE_RESERVATION_TRANSITION_INVALID") }
	r := e.Restore
	s := restoreState(events)
	if r == nil || e.RestoreSQL != nil || !identityPattern.MatchString(e.OperationID) || !identityPattern.MatchString(r.AttemptID) || !wire.ValidSHA256(r.PlanSHA256) || !wire.ValidSHA256(r.BindingSHA256) || e.Backup != nil || e.Engine != nil || e.Image != nil || e.Inputs != nil || e.Volume != nil || e.Runtime != nil || e.Process != nil || e.ModelTask != nil || e.ModelTaskAction != nil || e.DatabaseTask != nil || e.DatabaseTaskAction != nil {
		return bad()
	}
	if s.record == nil {
		if e.Action != "restore_reserve" || e.Outcome != "started" || e.ReferenceOperationID != "" || e.Code != "" || !databaseCreationProcessReady(events) || databaseTaskState(events).record != nil || modelTaskState(events).record != nil || backupState(events).record != nil || len(imageState(events).pending) > 0 || len(volumeState(events).pending) > 0 || runtimeState(events).pending != "" || !runtimeState(events).completed {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.aborted || *s.record != *r {
		return bad()
	}
	if e.Action == "restore_abort_reservation" && e.Outcome == "started" {
		if restoreSQLState(events).record != nil {
			return bad()
		}
		if s.action == "restore_abort_reservation" || e.ReferenceOperationID != s.pending || e.Code != "" {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
		return nil
	}
	if s.pending == "" {
		return bad()
	}
	if e.Action == "restore_reserve" {
		if s.action != "restore_reserve" || s.outcome != "started" || e.OperationID != s.pending || e.ReferenceOperationID != "" {
			return bad()
		}
	} else if e.Action == "restore_reconcile_reservation" {
		if s.action != "restore_reserve" || e.ReferenceOperationID != s.pending {
			return bad()
		}
		for _, prior := range events {
			if prior.OperationID == e.OperationID {
				return bad()
			}
		}
	} else if e.Action == "restore_abort_reservation" {
		if s.action != e.Action || e.OperationID != s.pending || e.ReferenceOperationID != s.abortReference {
			return bad()
		}
	} else {
		return bad()
	}
	if e.Outcome == "completed" && e.Code == "" {
		return nil
	}
	if codePattern.MatchString(e.Code) && ((e.Action == "restore_reserve" || e.Action == "restore_abort_reservation") && e.Outcome == "uncertain" || e.Action == "restore_reconcile_reservation" && e.Outcome == "unresolved") {
		return nil
	}
	return bad()
}

func restoreMutationGuard(events []Event, e Event) error {
	s := restoreState(events)
	if e.RestoreStores != nil || e.RestoreDatabase != nil || e.Restore != nil || e.RestoreSQL != nil {
		return failure("OFFLINE_SCOPE_RESTORE_RESERVATION_TRANSITION_INVALID")
	}
	if s.record == nil || s.aborted {
		return nil
	}
	// Only the explicit restored backup contract can enter the backup
	// validator. That validator checks admission, immutable phases and pending
	// effect references; this does not reopen ordinary initial backup access.
	if e.Backup != nil && e.Backup.RestoredAdmission != nil && (e.Action == "backup_effect" || e.Action == "backup_reconcile" || e.Action == "backup_abort") {
		return nil
	}
	// A stopped dependency may be recovered without activating the application.
	if e.Action == "process_stop" {
		return nil
	}
	if (e.Action == "process_start" || e.Action == "process_reconcile") && e.Process != nil && e.Process.RestoredApplication != nil && validApplicationProcess(events, *e.Process) {
		return nil
	}
	if e.Action == "process_start" && e.Process != nil && slices.Equal(e.Process.SelectedServices, []string{"postgres"}) {
		return nil
	}
	if e.Action == "process_reconcile" && e.Process != nil && (e.Process.Target == "stopped" || slices.Equal(e.Process.SelectedServices, []string{"postgres"})) {
		return nil
	}
	return failure("OFFLINE_SCOPE_RESTORE_EXECUTION_OR_ABORT_REQUIRED")
}
