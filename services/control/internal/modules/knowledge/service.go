package knowledge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/url"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func (s *Service) policy() config.KnowledgePolicy { return s.Identity.Config.Policy().Knowledge }
func admin(a identity.Auth) bool                  { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func canWrite(a identity.Auth, w workspaces.Workspace) bool {
	return admin(a) || a.Me.Audience == "portal" && w.Role != nil && (*w.Role == "owner" || *w.Role == "member")
}
func validID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}
func validReason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 500 && !strings.ContainsFunc(value, unicode.IsControl)
}
func normalizedName(value string, maximum int) (string, string, error) {
	value = strings.TrimSpace(norm.NFC.String(value))
	if !utf8.ValidString(value) || value == "" || utf8.RuneCountInString(value) > maximum || strings.ContainsFunc(value, unicode.IsControl) {
		return "", "", failure("VALIDATION_FAILED")
	}
	key := cases.Fold().String(norm.NFKC.String(value))
	if utf8.RuneCountInString(key) > maximum*2 {
		return "", "", failure("VALIDATION_FAILED")
	}
	return value, key, nil
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	return s.transactionBudget(ctx, a, workspace, write, s.policy().CommitSeconds, fn)
}
func (s *Service) queryTransaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	return s.transactionBudget(ctx, a, workspace, write, s.policy().QuerySeconds, fn)
}
func (s *Service) transactionBudget(ctx context.Context, a identity.Auth, workspace string, write bool, seconds int, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !validID(workspace) || a.Me.Audience != "portal" && !admin(a) {
		return failure("ACCESS_DENIED")
	}
	repo, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(seconds))
	defer cancel()
	e = repo.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" || write && !canWrite(a, w) {
			return failure("ACCESS_DENIED")
		}
		return fn(ctx, q, w)
	})
	return knowledgeError(e)
}
func (s *Service) enabled() error {
	if !s.Identity.Config.Knowledge.Enabled || !s.Processor.Available() {
		return failure("KNOWLEDGE_UNAVAILABLE")
	}
	return nil
}
func (s *Service) audit(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, id, reason, request string) error {
	_, e := tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success',$7,$8,$9,$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,?,?,'success',?,?,?,?)`, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, request}, []any{uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, request}))
	return e
}
func (s *Service) fingerprint(a identity.Auth, purpose string, input any) []byte {
	raw, _ := json.Marshal(struct {
		Actor, Audience, Purpose string
		Input                    any
	}{a.Me.User.ID, a.Me.Audience, purpose, input})
	return security.MAC(s.Identity.HMACKey, "knowledge-mutation", "v1", string(raw))
}
func (s *Service) intent(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, purpose, key string, input any) (string, []byte, error) {
	if !validID(key) {
		return "", nil, failure("VALIDATION_FAILED")
	}
	digest := s.fingerprint(a, purpose, input)
	var id, storedPurpose string
	var stored []byte
	e := tx.QueryRow(ctx, knowledgeSQL(tx, `SELECT entity_id::text,purpose,request_hash FROM knowledge_mutation_intents WHERE tenant_id=$1 AND workspace_id=$2 AND actor_id=$3 AND idempotency_key=$4 FOR UPDATE`, `SELECT entity_id,purpose,request_hash FROM knowledge_mutation_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=? FOR UPDATE`, []any{w.TenantID, w.ID, a.Me.User.ID, key}, []any{w.TenantID, w.ID, a.Me.User.ID, key})).Scan(&id, &storedPurpose, &stored)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", digest, nil
	}
	if e != nil {
		return "", nil, e
	}
	if purpose != storedPurpose || subtle.ConstantTimeCompare(digest, stored) != 1 {
		return "", nil, failure("IDEMPOTENCY_CONFLICT")
	}
	return id, digest, nil
}
func saveIntent(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, purpose, key, id string, digest []byte) error {
	_, e := tx.Exec(ctx, knowledgeSQL(tx, `INSERT INTO knowledge_mutation_intents(tenant_id,workspace_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES($1,$2,$3,$4,$5,$6,$7)`, `INSERT INTO knowledge_mutation_intents(tenant_id,workspace_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,?,?,?)`, []any{w.TenantID, w.ID, a.Me.User.ID, key, purpose, digest, id}, []any{w.TenantID, w.ID, a.Me.User.ID, key, purpose, digest, id}))
	return e
}
func delegation(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace) (*string, error) {
	if admin(a) {
		return nil, nil
	}
	var value string
	e := tx.QueryRow(ctx, knowledgeSQL(tx, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", `SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?`, []any{w.TenantID, w.ID, a.Me.User.ID}, []any{w.TenantID, w.ID, a.Me.User.ID})).Scan(&value)
	return &value, e
}
func (s *Service) pageValues(values url.Values) url.Values {
	out := url.Values{}
	for k, v := range values {
		out[k] = append([]string{}, v...)
	}
	if _, ok := out["limit"]; !ok {
		out.Set("limit", strconv.Itoa(s.Identity.Config.Policy().UI.PageSize))
	}
	return out
}
