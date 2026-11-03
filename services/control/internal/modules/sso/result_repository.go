package sso

import (
	"context"
	"crypto/hmac"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const flowBrowserBindingPurpose = "m11-flow-browser-context-v1"

// The proof binds the exact registered callback, including its independently
// selected origin/port/path. A changed deployment cannot exchange an old flow.
func (s *Service) browserProof(connector, audience, browser string) []byte {
	callback, e := s.callback(connector, audience)
	if e != nil {
		return nil
	}
	return security.MAC(s.Identity.HMACKey, flowBrowserBindingPurpose, callback, browser)
}

// BoundResultMode selects only a fixed error presentation after failed claim.
// It grants no current account, binding, exchange, retry or session authority.
// Terminal/revoked flows may still explain their failure to their exact browser.
func (s *Service) BoundResultMode(ctx context.Context, connector, audience, state, browser, previous string) string {
	if ctx == nil || s == nil || s.Identity == nil || !canonicalID(connector) || (audience != "admin" && audience != "portal") || !security.ValidToken(state) || !security.ValidToken(browser) {
		return ""
	}
	store, e := s.catalogRepository()
	if e != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy.OperationSeconds))
	defer cancel()
	var mode string
	var browserHash, previousHash []byte
	e = store.QueryRow(ctx, sourceSQL(store,
		"SELECT mode,browser_hash,previous_cookie_hash FROM sso_flows WHERE connector_id=$1 AND audience=$2 AND state_hash=$3",
		"SELECT mode,browser_hash,previous_cookie_hash FROM sso_flows WHERE connector_id=? AND audience=? AND state_hash=?",
		"SELECT MODE,BROWSER_HASH,PREVIOUS_COOKIE_HASH FROM SSO_FLOWS WHERE CONNECTOR_ID=? AND AUDIENCE=? AND STATE_HASH=?", connector, audience, security.Digest(state))).Scan(&mode, &browserHash, &previousHash)
	if e != nil || (mode != ModeLogin && mode != ModeLink) || !hmac.Equal(browserHash, s.browserProof(connector, audience, browser)) || !hmac.Equal(previousHash, security.Digest(security.Binding(previous))) {
		return ""
	}
	return mode
}
