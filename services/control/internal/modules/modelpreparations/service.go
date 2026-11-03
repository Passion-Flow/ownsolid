package modelpreparations

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type DatasetSources interface {
	PublishedSourcesAvailable() bool
	PublishedSourceSnapshot(context.Context, identity.Auth, string, string, string, bool) (datasets.Version, error)
	PublishedSourceInTransaction(context.Context, pgx.Tx, string, string, string, string, bool) (datasets.Version, error)
	PublishedSourceWithin(context.Context, transaction.Executor, string, string, string, string, bool) (datasets.Version, error)
	PublishedSourceSize(datasets.Version) (int64, error)
	OpenPublishedSource(context.Context, datasets.Version, int64) (*os.File, int64, error)
}

type Service struct {
	Inference  *deployments.Service
	Training   *training.Service
	Nodes      *nodes.Service
	Models     *models.Service
	Store      *Store
	DB         *pgxpool.Pool
	Identity   *identity.Service
	Workspaces *workspaces.Service
	Datasets   DatasetSources
	scanSlots  chan struct{}
}

func New(db *pgxpool.Pool, identityService *identity.Service, workspaceService *workspaces.Service, datasetService *datasets.Service) (*Service, error) {
	if identityService == nil || workspaceService == nil || workspaceService.Identity != identityService {
		return nil, fail("MODEL_HOLDOUT_UNAVAILABLE")
	}
	if db != nil && db != identityService.DB || workspaceService.DB != nil && workspaceService.DB != identityService.DB {
		return nil, fail("MODEL_PREPARATION_STORAGE_MISMATCH")
	}
	repository, e := identityService.Repository()
	if e != nil || db == nil && repository.Dialect() != transaction.MySQL && repository.Dialect() != transaction.Dameng {
		return nil, fail("MODEL_HOLDOUT_UNAVAILABLE")
	}
	policy := identityService.Config.Policy().ModelLifecycle
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	value := &Service{DB: db, Identity: identityService, Workspaces: workspaceService, scanSlots: make(chan struct{}, policy.ScanConcurrency)}
	if datasetService != nil {
		value.Datasets = datasetService
	}
	return value, nil
}
func fail(code string) error     { return errors.New(code) }
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func owner(a identity.Auth, w workspaces.Workspace) bool {
	return admin(a) || a.Me.Audience == "portal" && w.Role != nil && *w.Role == "owner"
}
func text(value string, maximum int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
func (s *Service) policy() config.ModelLifecyclePolicy {
	return s.Identity.Config.Policy().ModelLifecycle
}
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, pgx.Tx, workspaces.Workspace) error) error {
	if s == nil || s.DB == nil {
		return fail("MODEL_PREPARATION_UNAVAILABLE")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.policy().TransactionSeconds))
	defer cancel()
	if !datawire.ValidID(workspace) || a.Me.Audience != "portal" && !admin(a) {
		return fail("ACCESS_DENIED")
	}
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	defer tx.Rollback(ctx)
	if e = s.Identity.Recheck(ctx, tx, a); e != nil {
		return e
	}
	scope, e := s.Workspaces.RecheckScope(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if scope.Status != "active" || write && !owner(a, scope) {
		return fail("ACCESS_DENIED")
	}
	if e = fn(ctx, tx, scope); e != nil {
		return mapped(e)
	}
	if e = tx.Commit(ctx); e != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}
func mapped(e error) error {
	var databaseError *pgconn.PgError
	if errors.As(e, &databaseError) {
		if databaseError.ConstraintName == "model_holdout_version_unique" {
			return fail("MODEL_HOLDOUT_EXISTS")
		}
		if databaseError.ConstraintName == "model_holdout_creation_unique" {
			return fail("IDEMPOTENCY_CONFLICT")
		}
		return fail("STORAGE_UNAVAILABLE")
	}
	return e
}
func hash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func fingerprint(a identity.Auth, input any) []byte {
	raw, _ := json.Marshal(struct {
		Actor    string
		Audience string
		Input    any
	}{a.Me.User.ID, a.Me.Audience, input})
	sum := sha256.Sum256(raw)
	return sum[:]
}
func same(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
func (s *Service) acquireScan(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	select {
	case s.scanSlots <- struct{}{}:
		return func() { <-s.scanSlots }, nil
	default:
		return nil, fail("MODEL_HOLDOUT_SCAN_BUSY")
	}
}
func audit(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','model_holdout',$7,$8,$9)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, id, reason, request)
	return e
}
func validLabel(value string) bool {
	for _, label := range wire.Labels() {
		if value == label {
			return true
		}
	}
	return false
}
