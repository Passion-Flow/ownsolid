package v1

import "testing"

func TestAcceptLeaseRefusesOutOfOrderSequences(t *testing.T) {
	cases := []struct {
		name     string
		cursor   uint64
		sequence uint64
		accepted bool
	}{
		{"first task", 0, 1, true},
		{"next task", 4, 5, true},
		{"later task after released ones", 4, 9, true},
		{"same task delivered again", 5, 5, true},
		{"older task", 5, 4, false},
		{"beyond the JSON range", 0, MaxTaskSequence + 1, false},
	}
	for _, c := range cases {
		if e := AcceptLease(c.cursor, Lease{Sequence: c.sequence}); (e == nil) != c.accepted {
			t.Errorf("%s: %v", c.name, e)
		}
	}
}

func TestClaimHoldsALeaseOrAResync(t *testing.T) {
	if (Claim{}).Validate() != nil || (Claim{Lease: &Lease{}}).Validate() != nil || (Claim{Resync: &Resync{AcknowledgedSequence: 2}}).Validate() != nil {
		t.Fatal("refused a valid claim")
	}
	if (Claim{Lease: &Lease{}, Resync: &Resync{}}).Validate() == nil || (Claim{Resync: &Resync{AcknowledgedSequence: MaxTaskSequence + 1}}).Validate() == nil {
		t.Fatal("accepted an invalid claim")
	}
}
