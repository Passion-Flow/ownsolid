package scope

// RecordedProcessStatus contains journal facts only. It never asserts that a
// recorded process is still running, healthy or authorized for a new action.
type RecordedProcessStatus struct {
	RetryOfOperationID      string   `json:"retry_of_operation_id,omitempty"`
	RecoveryOfOperationID   string   `json:"recovery_of_operation_id,omitempty"`
	RecoveryStopOperationID string   `json:"recovery_stop_operation_id,omitempty"`
	Submission              string   `json:"submission,omitempty"`
	OperationID             string   `json:"operation_id"`
	Action                  string   `json:"action"`
	Target                  string   `json:"target"`
	SelectedServices        []string `json:"selected_services"`
	Outcome                 string   `json:"outcome"`
	Pending                 bool     `json:"pending"`
	Qualification           string   `json:"qualification"`
}

func recordedProcessStatus(events []Event, applicationOnly bool) *RecordedProcessStatus {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Process == nil || applicationOnly && e.Process.RestoredApplication == nil || e.Action != "process_start" && e.Action != "process_stop" && e.Action != "process_reconcile" {
			continue
		}
		id := e.OperationID
		if e.Action == "process_reconcile" {
			id = e.ReferenceOperationID
		}
		return &RecordedProcessStatus{RetryOfOperationID: e.Process.RetryOfOperationID, RecoveryOfOperationID: e.Process.RecoveryOfOperationID, RecoveryStopOperationID: e.Process.RecoveryStopOperationID, Submission: e.Process.Submission, OperationID: id, Action: e.Action, Target: e.Process.Target, SelectedServices: append([]string{}, e.Process.SelectedServices...), Outcome: e.Outcome, Pending: e.Outcome == "started" || e.Outcome == "uncertain" || e.Outcome == "unresolved", Qualification: "historical_journal_fact_only_not_current_process_health_activation_or_target"}
	}
	return nil
}

func applyRecordedProcessStatus(s *Status, events []Event) {
	s.Process = recordedProcessStatus(events, false)
	s.RestoredApplication = recordedProcessStatus(events, true)
	if s.Process != nil && s.Process.Pending {
		s.Status = "isolated_scope_process_action_uncertain"
		s.Qualification = s.Process.Qualification
	}
}
