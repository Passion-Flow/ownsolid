package workflows

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"

// An explicitly enabled but unavailable knowledge runtime must never fall back to tools.
func (s *Service) toolOnly() bool {
	return !s.Identity.Config.Knowledge.Enabled && s.Identity.Config.Plugins.Enabled
}

func (s *Service) questionBytes() int {
	maximum := s.policy().QuestionBytes
	if !s.toolOnly() {
		maximum = min(maximum, s.Identity.Config.Policy().Knowledge.QueryBytes)
	}
	return maximum
}

func (s *Service) parseGraph(raw []byte, policy config.WorkflowPolicy, maximumTopK int) (ParsedGraph, error) {
	return ParseGraph(raw, policy, maximumTopK)
}
