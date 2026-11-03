package database_test

import "testing"

// This deliberately tests the frozen OLD1–18 schema. It must never be reported
// as the current product's acceptable retirement behavior after correction.
func TestDatasetRetirementFrozen18NullReasonReproduction(t *testing.T) {
	s := retirementFrozen18(t)
	r := retirementSeed(t, s.DB)
	journal := retirementJournalPrefix(t, s.DB)
	for _, item := range []struct{ table, id string }{{"datasets", r.dataset}, {"dataset_versions", r.version}} {
		t.Run(item.table, func(t *testing.T) {
			retirementExec(t, s.DB, "UPDATE "+item.table+" SET retired_at=clock_timestamp(),retire_reason=NULL WHERE id=$1", item.id)
			var admitted bool
			if e := s.DB.QueryRow(t.Context(), "SELECT retired_at IS NOT NULL AND retire_reason IS NULL FROM "+item.table+" WHERE id=$1", item.id).Scan(&admitted); e != nil || !admitted {
				t.Fatal("frozen schema NULL-reason reproduction did not commit", e)
			}
		})
	}
	if retirementJournalPrefix(t, s.DB) != journal {
		t.Fatal("reproduction changed frozen migration journal")
	}
	t.Log("FROZEN_POSTGRES18_RETIREMENT_NULL_REASON_ADMITTED datasets=1;dataset_versions=1;business_sha256=" + retirementBusinessDigest(t, s.DB))
}
