package workflows

import "github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"

// The selected profile is immutable deployment configuration. An explicitly
// enabled but unavailable knowledge runtime must never fall back to tools.
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

// parseGraph keeps the current composed public capability at schema1 even though
// later platform source supports other explicitly selected graph families.
func (s *Service) parseGraph(raw []byte, policy config.WorkflowPolicy, maximumTopK int) (ParsedGraph, error) {
	p, e := ParseGraph(raw, policy, maximumTopK)
	if e == nil && s.Identity.Config.WorkflowsOnly() && p.Graph.SchemaVersion != GraphSchemaVersion {
		return ParsedGraph{}, graphFailure()
	}
	return p, e
}
