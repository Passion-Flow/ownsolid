package training

// EnableSchedulingCandidateForTest selects the borrowed reservation path in an
// isolated fixture that has applied the scheduling candidate schema.
func (s *Service) EnableSchedulingCandidateForTest() {
	s.schedulingReservations = true
}
