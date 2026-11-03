package database_test

import "testing"

// This confirms defective historical SQL behavior, not acceptable product output.
func TestModelReviewFrozen19NullApprovalReproduction(t *testing.T) {
	s := modelReviewFrozen19(t)
	r := modelReviewSeed(t, s.DB)
	journal := modelReviewJournal(t, s.DB)
	arguments := modelReviewArguments(r, "approve", nil, "Synthetic approval metadata")
	retirementExec(t, s.DB, modelReviewInsert, arguments...)
	var admitted bool
	if e := s.DB.QueryRow(t.Context(), `SELECT decision='approve' AND model_id IS NULL FROM model_preparation_reviews WHERE id=$1`, arguments[0]).Scan(&admitted); e != nil || !admitted {
		t.Fatal("frozen NULL approval did not actually commit", e)
	}
	if modelReviewJournal(t, s.DB) != journal {
		t.Fatal("reproduction changed immutable migration prefix")
	}
	t.Log("FROZEN_POSTGRES19_MODEL_REVIEW_NULL_APPROVAL_ADMITTED=1;business_sha256=" + retirementBusinessDigest(t, s.DB))
}
