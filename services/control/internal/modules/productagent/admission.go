package productagent

import "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"

type riskTier string

const (
	riskNavigate        riskTier = "navigate"
	riskRead            riskTier = "read"
	riskSensitiveRead   riskTier = "sensitive_read"
	riskReversibleWrite riskTier = "reversible_write"
)

// admit applies the compiled command policy to discovery and execution. The
// operation still verifies the session and workspace in its own transaction.
func (s *Service) admit(cmd command, actor identity.Auth, cursor string) (string, error) {
	var path string
	policy, hasTool := s.tools[cmd.id]
	switch cmd.risk {
	case riskNavigate:
		path = cmd.destination(actor.Me.Audience)
		if hasTool || path == "" {
			return "", ErrCommandDenied
		}
		if cursor != "" {
			return "", ErrInvalidCommand
		}
	case riskRead:
		if cmd.admin != "" || cmd.portal != "" || !policy.allowsRead(actor.Me.Audience) {
			return "", ErrCommandDenied
		}
	case riskSensitiveRead, riskReversibleWrite:
		// Sensitive reads need their own authorized projection. Writes need
		// a signed plan and a separate human confirmation.
		return "", ErrCommandDenied
	default:
		return "", ErrCommandDenied
	}
	if actor.Me.Workspace == nil {
		return "", ErrWorkspaceRequired
	}
	return path, nil
}
