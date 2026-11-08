package identity

import (
	"context"
	"net/url"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AuditAssistantActor is the verified built-in assistant identity for an audit event.
type AuditAssistantActor struct {
	Kind string
	ID   string
}

// AuditEventKey identifies one stored event using its ID and public occurred_at timestamp.
type AuditEventKey struct {
	ID         string
	OccurredAt string
}

// WorkspaceAuditList pairs the existing audit items with assistant attribution by event key.
// The attribution is repository-only until the public contract defines it.
type WorkspaceAuditList struct {
	List
	AssistantActors map[AuditEventKey]AuditAssistantActor `json:"-"`
}

type workspaceAuditProjection struct {
	workspaceID string
	actors      map[AuditEventKey]AuditAssistantActor
}

const (
	auditAssistantKind = "product_assistant"
	auditAssistantID   = "ownsolid.product-assistant"

	installedAuditTenant = "SELECT i.tenant_id FROM installation_state i WHERE i.id=1 AND i.bootstrap_complete"
)

// ListWorkspaceAuditForActor reads audit events in one workspace of the installed
// tenant. Only a current platform Admin session can select a workspace.
func (s *Service) ListWorkspaceAuditForActor(ctx context.Context, a Auth, workspaceID string, q url.Values) (WorkspaceAuditList, error) {
	projection := workspaceAuditProjection{workspaceID: workspaceID, actors: make(map[AuditEventKey]AuditAssistantActor)}
	list, err := s.listForActor(ctx, a, "audit-events", q, &projection)
	if err != nil {
		return WorkspaceAuditList{List: list}, err
	}
	return WorkspaceAuditList{List: list, AssistantActors: projection.actors}, nil
}

func (p *workspaceAuditProjection) authorize(ctx context.Context, q transaction.Executor) error {
	if q.Dialect() != transaction.PostgreSQL {
		return failure("AUDIT_DIALECT_UNSUPPORTED")
	}
	const scope = "SELECT EXISTS(SELECT 1 FROM workspaces w WHERE w.id=$1 AND w.tenant_id=(" + installedAuditTenant + "))"
	var found bool
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, scope, p.workspaceID)).Scan(&found); err != nil {
		return err
	}
	if !found {
		return failure("RESOURCE_NOT_FOUND")
	}
	return nil
}

// The runtime role cannot read audit_chain_head. Match the row's V2 commitment
// using migration 000054's domain and field order, with its canonical projection
// and framing functions. CASE avoids hashing non-candidates.
const auditAssistantProjectionSQL = `
	CASE WHEN a.chain_position IS NOT NULL AND a.actor_user_id IS NULL AND a.subject_user_id IS NOT NULL
		AND a.audience IN ('admin', 'portal')
		AND a.action COLLATE "C" ~ '^product_assistant\.[a-z][a-z0-9_]*$'
		AND a.metadata = '{"actor_kind":"` + auditAssistantKind + `","actor_id":"` + auditAssistantID + `"}'::jsonb
	THEN a.hash = (
		SELECT sha256(convert_to('ownsolid.audit.v2', 'UTF8') || decode('00', 'hex') || a.prev_hash
			|| string_agg(audit_chain_frame(projection ->> field), ''::bytea ORDER BY ordinal))
		FROM audit_chain_projection_v2(to_jsonb(a), a.chain_position, a.occurred_at, a.identifier_digest) AS p(projection)
		CROSS JOIN unnest(ARRAY[
			'position', 'id', 'occurred_at', 'actor_user_id', 'subject_user_id', 'tenant_id', 'workspace_id',
			'audience', 'action', 'outcome', 'target_type', 'target_id', 'request_id',
			'reason_sha256', 'identifier_digest_sha256', 'metadata_sha256', 'site_id', 'cell_id'
		]) WITH ORDINALITY AS fields(field, ordinal)
	)
	ELSE false END`
