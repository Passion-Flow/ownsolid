package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestProcessStatusPreservesHistoryWithoutImplyingHealth(t *testing.T) {
	if recordedProcessStatus(nil, false) != nil {
		t.Fatal("invented process history")
	}
	r := &ProcessRecord{Target: "running", SelectedServices: []string{"control"}, RestoredApplication: &RestoredApplicationAdmission{}}
	events := []Event{{OperationID: "original", Action: "process_start", Outcome: "started", Process: r}, {OperationID: "observation", ReferenceOperationID: "original", Action: "process_reconcile", Outcome: "unresolved", Process: r}}
	s := Status{Status: "isolated_scope_stores_qualified"}
	applyRecordedProcessStatus(&s, events)
	if s.Status != "isolated_scope_process_action_uncertain" || s.Process == nil || !s.Process.Pending || s.Process.OperationID != "original" || s.Process.Action != "process_reconcile" || s.RestoredApplication == nil || !s.RestoredApplication.Pending {
		t.Fatal("pending process hidden by completed stage", s)
	}
	s.Process.SelectedServices[0] = "foreign"
	if r.SelectedServices[0] != "control" {
		t.Fatal("status aliases journal")
	}
	events = append(events, Event{OperationID: "observation2", ReferenceOperationID: "original", Action: "process_reconcile", Outcome: "completed", Process: r}, Event{OperationID: "shutdown", Action: "process_stop", Outcome: "completed", Process: &ProcessRecord{Target: "stopped"}})
	s = Status{Status: "historical_stage"}
	applyRecordedProcessStatus(&s, events)
	if s.Process.Pending || s.Process.Target != "stopped" || s.Process.OperationID != "shutdown" || s.RestoredApplication.Pending || s.RestoredApplication.OperationID != "original" || s.RestoredApplication.Outcome != "completed" || s.Status != "historical_stage" {
		t.Fatal("stopped history misreported", s)
	}
	raw, _ := json.Marshal(s)
	if bytes.Contains(raw, []byte(`"healthy"`)) || bytes.Contains(raw, []byte(`"running":true`)) {
		t.Fatal("invented current health")
	}
}

func TestProcessStatusActualPrivateJournalExposesPendingApplicationAfterStoreProof(t *testing.T) {
	c, _, d, w := applicationFixture(t)
	w.controlFault = "after"
	if out, e := startApplicationDouble(t, c, d, w); e == nil || !out.Pending {
		t.Fatal("explicit response-loss double must be pending", out, e)
	}
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	s, e := Read(context.Background(), c)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || !bytes.Equal(before, after) || s.RestoredCurrentStores == nil || s.Process == nil || !s.Process.Pending || s.RestoredApplication == nil || !s.RestoredApplication.Pending || s.Status != "isolated_scope_process_action_uncertain" {
		t.Fatal("readonly reopened journal lost pending process", s, e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "", true); e != nil {
		t.Fatal(e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "stopped", false); e != nil {
		t.Fatal(e)
	}
	s, e = Read(context.Background(), c)
	if e != nil || s.Process == nil || s.Process.Pending || s.Process.Target != "stopped" || s.RestoredApplication == nil || s.RestoredApplication.Pending || s.RestoredApplication.Outcome != "completed" {
		t.Fatal("completed activation history must survive shutdown", s, e)
	}
}
