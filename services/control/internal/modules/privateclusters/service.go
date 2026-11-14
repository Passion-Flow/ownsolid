package privateclusters

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const MaximumRevision int64 = 9007199254740991

type Service struct {
	identity   *identity.Service
	workspaces *workspaces.Service
	license    *licensing.Service
	config     Config
}

// UsesOwners lets the installed composition verify the immutable service graph.
func (s *Service) UsesOwners(i *identity.Service, w *workspaces.Service, l *licensing.Service) bool {
	return s != nil && i != nil && w != nil && l != nil && s.identity == i && s.workspaces == w &&
		s.license == l && w.Identity == i && l.UsesIdentity(i)
}

func (s *Service) OperationBudgetSeconds() int {
	if s == nil {
		return 0
	}
	return s.config.Policy.OperationSeconds
}

// MaximumResponseBytes is the existing owning body budget, applied to the whole
// public JSON response as well as each provider exchange. It is not a second
// configuration authority or a browser override.
func (s *Service) MaximumResponseBytes() int64 {
	if s == nil {
		return 0
	}
	return s.config.Policy.MaximumBodyBytes
}

type Mutation struct {
	WorkspaceID      string `json:"-"`
	Key              string `json:"-"`
	RequestID        string `json:"-"`
	Reason           string `json:"reason"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (m Mutation) validate() error {
	if !validID(m.WorkspaceID) || !validID(m.Key) || !validID(m.RequestID) || m.ExpectedRevision < 0 || m.ExpectedRevision >= MaximumRevision || strings.TrimSpace(m.Reason) != m.Reason || !utf8.ValidString(m.Reason) || utf8.RuneCountInString(m.Reason) < 1 || utf8.RuneCountInString(m.Reason) > 500 || strings.ContainsFunc(m.Reason, unicode.IsControl) {
		return fail("VALIDATION_FAILED")
	}
	return nil
}
func New(i *identity.Service, w *workspaces.Service, l *licensing.Service, c Config) (*Service, error) {
	if i == nil || w == nil || w.Identity != i || l == nil || !l.UsesIdentity(i) {
		return nil, fail("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	if c.Validate() != nil {
		return nil, fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	if _, e := i.Repository(); e != nil {
		return nil, e
	}
	return &Service{i, w, l, c}, nil
}
func vendorStatement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.Statement{}
	}
}
func statement(q transaction.Executor, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() == transaction.MySQL {
		return transaction.SQL(transaction.MySQL, my, args...)
	}
	return transaction.SQL(transaction.PostgreSQL, pg, args...)
}
func safeError(e error) error {
	if e == nil {
		return nil
	}
	// Current main owns the bounded joined-error classifier. Unknown COMMIT
	// precedes cancellation and known rollback; malformed graphs fail closed.
	if kind, ok := transaction.CommitOutcome(e); ok {
		return fail(string(kind))
	}
	if errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	var tx *transaction.Error
	if errors.As(e, &tx) {
		return fail(string(tx.Kind))
	}
	code := e.Error()
	for _, p := range []string{"PRIVATE_CLUSTER_", "LICENSE_", "AUTH_", "STORAGE_", "REPOSITORY_"} {
		if strings.HasPrefix(code, p) && !strings.ContainsAny(code, " \t\r\n:") {
			return fail(code)
		}
	}
	switch code {
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "VERSION_CONFLICT":
		return fail(code)
	}
	return fail("STORAGE_UNAVAILABLE")
}
func (s *Service) actor(ctx context.Context, a identity.Auth, workspace string, admin, write, business bool, fn func(context.Context, transaction.Executor, workspaces.Workspace, *string) error) error {
	if s == nil || s.identity == nil || s.license == nil || s.workspaces == nil || ctx == nil || fn == nil || !validID(workspace) || a.Me.Audience != "admin" && a.Me.Audience != "portal" || admin && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return safeError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.config.Policy.OperationSeconds)*time.Second)
	defer cancel()
	if business {
		if e = s.license.Check(ctx); e != nil {
			return safeError(e)
		}
	}
	return safeError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		var authority *string
		if a.Me.Audience == "portal" {
			if w.Role == nil || write && *w.Role != "owner" && *w.Role != "member" {
				return fail("ACCESS_DENIED")
			}
			value, e := s.workspaces.MembershipAuthorityWithin(ctx, q, w.TenantID, w.ID, a.Me.User.ID)
			if e != nil {
				return e
			}
			authority = &value
		}
		if business {
			if e = s.license.CheckWithin(ctx, q); e != nil {
				return e
			}
		}
		// The installation/license singleton precedes connection/run locks for
		// both new work and safety recovery, avoiding a lock-order inversion.
		var installation string
		if e = q.QueryRow(ctx, statement(q, `SELECT deployment_id::text FROM platform_license_state WHERE id=1 FOR SHARE`, `SELECT deployment_id FROM platform_license_state WHERE id=1 FOR SHARE`)).Scan(&installation); e != nil {
			return e
		}
		if !validID(installation) {
			return fail("PRIVATE_CLUSTER_INSTALLATION_MISMATCH")
		}
		return fn(ctx, q, w, authority)
	}))
}
func auditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target string, m Mutation) error {
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES($1,clock_timestamp(),$2,$3,$4,'success','private_cluster',$5,$6,$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,target_type,target_id,reason,request_id,tenant_id,workspace_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'success','private_cluster',?,?,?,?,?)`, uuid.NewString(), a.Me.User.ID, a.Me.Audience, "private_cluster."+action, target, m.Reason, m.RequestID, w.TenantID, w.ID))
	return e
}

type intent struct {
	target   string
	revision int64
}

func replayWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, op string, m Mutation, input any) (intent, bool, error) {
	var previous intent
	var action, hash string
	e := q.QueryRow(ctx, statement(q, `SELECT operation,fingerprint,target_id::text,result_revision FROM private_cluster_intents WHERE tenant_id=$1 AND workspace_id=$2 AND actor_id=$3 AND idempotency_key=$4 FOR UPDATE`, `SELECT operation,fingerprint,target_id,result_revision FROM private_cluster_intents WHERE tenant_id=? AND workspace_id=? AND actor_id=? AND idempotency_key=? FOR UPDATE`, w.TenantID, w.ID, a.Me.User.ID, m.Key)).Scan(&action, &hash, &previous.target, &previous.revision)
	if errors.Is(e, sql.ErrNoRows) {
		return intent{}, false, nil
	}
	if e != nil {
		return intent{}, false, e
	}
	if action != op || hash != fingerprint(input) {
		return intent{}, false, fail("IDEMPOTENCY_CONFLICT")
	}
	return previous, true, nil
}
func recordIntent(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, op, target string, revision int64, m Mutation, input any) error {
	_, e := q.Exec(ctx, statement(q, `INSERT INTO private_cluster_intents(tenant_id,workspace_id,actor_id,idempotency_key,operation,fingerprint,target_id,result_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, `INSERT INTO private_cluster_intents(tenant_id,workspace_id,actor_id,idempotency_key,operation,fingerprint,target_id,result_revision) VALUES(?,?,?,?,?,?,?,?)`, w.TenantID, w.ID, a.Me.User.ID, m.Key, op, fingerprint(input), target, revision))
	if e != nil {
		return e
	}
	return auditWithin(ctx, q, a, w, op, target, m)
}
func (s *Service) client(ctx context.Context, w workspaces.Workspace, pid string) (*client, error) {
	p, e := s.config.profile(pid)
	if e != nil {
		return nil, e
	}
	if p.WorkspaceID != w.ID {
		return nil, fail("RESOURCE_NOT_FOUND")
	}
	return newClient(ctx, s.config, p)
}
func installationWithin(ctx context.Context, q transaction.Executor, p Profile) error {
	var deployment string
	e := q.QueryRow(ctx, statement(q, `SELECT deployment_id::text FROM platform_license_state WHERE id=1 FOR SHARE`, `SELECT deployment_id FROM platform_license_state WHERE id=1 FOR SHARE`)).Scan(&deployment)
	if e != nil {
		return e
	}
	if deployment != p.InstallationID {
		return fail("PRIVATE_CLUSTER_INSTALLATION_MISMATCH")
	}
	return nil
}
func sameNullable(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func original(r Run, a identity.Auth, authority *string) error {
	if r.actor != a.Me.User.ID || r.epoch != a.Epoch || r.session != a.SessionID || len(r.tokenHash) != 32 || subtle.ConstantTimeCompare(r.tokenHash, a.TokenHash) != 1 || !sameNullable(r.membership, authority) {
		return fail("PRIVATE_CLUSTER_ORIGIN_AUTHORITY_EXPIRED")
	}
	return nil
}
