package sso

import (
	"crypto/sha256"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

const mysqlFlowColumns = `id,tenant_id,connector_id,audience,mode,state,connector_revision,auth_version,browser_hash,previous_cookie_hash,previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,claim_id,expires_at`

func validPrivateFlow(f flow) bool {
	if !canonicalID(f.id) || !canonicalID(f.tenant) || !canonicalID(f.connector) || (f.audience != "portal" && f.audience != "admin") || f.connectorRevision < 1 || f.connectorRevision > identity.MaximumAuthVersion || f.authVersion < 1 || f.authVersion > identity.MaximumAuthVersion || len(f.browserHash) != sha256.Size || len(f.previousHash) != sha256.Size || !connectorTimestamp(f.expires) {
		return false
	}
	if f.previousSession != nil && !canonicalID(*f.previousSession) {
		return false
	}
	switch f.mode {
	case ModeLogin:
		if f.user != nil || f.session != nil || f.epoch != nil || f.credential != nil || f.reason != nil {
			return false
		}
	case ModeLink:
		if f.previousSession == nil || f.user == nil || !canonicalID(*f.user) || f.session == nil || !canonicalID(*f.session) || *f.previousSession != *f.session || f.epoch == nil || *f.epoch < 1 || *f.epoch > identity.MaximumAuthVersion || f.credential == nil || *f.credential < 1 || *f.credential > identity.MaximumAuthVersion || f.reason == nil || !validReason(*f.reason) {
			return false
		}
	default:
		return false
	}
	switch f.state {
	case flowPending:
		return f.claim == nil
	case flowClaimed, "succeeded":
		return f.claim != nil && canonicalID(*f.claim)
	case "failed", "expired":
		return f.claim == nil || canonicalID(*f.claim)
	default:
		return false
	}
}

func validClaimedFlow(f claimedFlow) bool {
	if !validPrivateFlow(f.flow) || f.state != flowClaimed {
		return false
	}
	if f.previousSession == nil {
		return f.previousActor == nil
	}
	return f.previousActor != nil && validSSOActor(*f.previousActor) && f.previousActor.Me.Audience == f.audience && f.previousActor.SessionID == *f.previousSession && (f.mode != ModeLink || f.previousActor.Me.User.ID == *f.user && f.previousActor.Epoch == *f.epoch)
}
