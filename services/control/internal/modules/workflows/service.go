package workflows

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaximumRevision int64 = 9007199254740991

func failure(code string) error                  { return errors.New(code) }
func (s *Service) policy() config.WorkflowPolicy { return s.Identity.Config.Policy().Workflows }
func admin(a identity.Auth) bool                 { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func editable(a identity.Auth, w workspaces.Workspace) bool {
	return admin(a) || a.Me.Audience == "portal" && w.Role != nil && (*w.Role == "owner" || *w.Role == "member")
}
func validReason(reason string) bool {
	return utf8.ValidString(reason) && strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 500 && !strings.ContainsFunc(reason, unicode.IsControl)
}
func nameKey(value string, maximum int) (string, string, error) {
	value = strings.TrimSpace(norm.NFC.String(value))
	key := cases.Fold().String(norm.NFKC.String(value))
	if !utf8.ValidString(value) || value == "" || utf8.RuneCountInString(value) > maximum || utf8.RuneCountInString(key) > maximum*2 || strings.ContainsFunc(value, unicode.IsControl) {
		return "", "", failure("VALIDATION_FAILED")
	}
	return value, key, nil
}
func (s *Service) audit(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, id, reason, request string) error {
	e := workflowExecOne(ctx, tx, workflowStatement(tx, workflowAudit, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, id, reason, request))
	return e
}
func (s *Service) intent(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, purpose, key string, input any) (string, []byte, error) {
	if !wire.ValidID(key) {
		return "", nil, failure("VALIDATION_FAILED")
	}
	raw, _ := json.Marshal(struct {
		Actor, Audience, Purpose string
		Input                    any
	}{a.Me.User.ID, a.Me.Audience, purpose, input})
	digest := security.MAC(s.Identity.HMACKey, "workflow-mutation", "v1", string(raw))
	var id, previousPurpose string
	var previous []byte
	e := tx.QueryRow(ctx, workflowStatement(tx, workflowReadIntent, w.TenantID, w.ID, a.Me.User.ID, key)).Scan(&id, &previousPurpose, &previous)
	if errors.Is(e, sql.ErrNoRows) {
		return "", digest, nil
	}
	if e != nil {
		return "", nil, e
	}
	if purpose != previousPurpose || subtle.ConstantTimeCompare(digest, previous) != 1 {
		return "", nil, failure("IDEMPOTENCY_CONFLICT")
	}
	return id, digest, nil
}
func saveIntent(ctx context.Context, tx transaction.Executor, a identity.Auth, w workspaces.Workspace, purpose, key, id string, digest []byte) error {
	e := workflowExecOne(ctx, tx, workflowStatement(tx, workflowSaveIntent, w.TenantID, w.ID, a.Me.User.ID, key, purpose, digest, id))
	return e
}
