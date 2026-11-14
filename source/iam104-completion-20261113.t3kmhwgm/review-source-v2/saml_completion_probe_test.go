//go:build repository_integration

package sso

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func selectSAMLConnectorWithin(ctx context.Context, q transaction.Executor, tenant, id string) (Connector, error) {
	return selectSAMLCompletionConnector(ctx, q, tenant, id)
}
func (p samlClaimProbe) complete(ctx context.Context, connector, audience, flow, continuation, previous, requestID string) (identity.IssuedSession, error) {
	owner, e := NewSAMLCompletion(ctx, p.service)
	if e != nil {
		return identity.IssuedSession{}, e
	}
	return owner.Complete(ctx, SAMLCompleteInput{ConnectorID: connector, Audience: audience, FlowID: flow, Continuation: continuation, PreviousSession: previous}, requestID)
}
