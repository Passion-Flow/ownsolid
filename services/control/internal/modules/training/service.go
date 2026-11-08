package training

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	Holdouts   HoldoutGuard
	DB         *pgxpool.Pool
	Identity   *identity.Service
	Workspaces *workspaces.Service
	Nodes      *nodes.Service
	Models     *models.Service
	Datasets   *datasets.Service
	Inference  *deployments.Service
	Store      *Store

	// Candidate-only until production composition can verify the migrated catalog
	// and account for attempts that already hold a physical lease.
	schedulingReservations bool
}

func fail(code string) error     { return errors.New(code) }
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func owner(a identity.Auth, w workspaces.Workspace) bool {
	return admin(a) || a.Me.Audience == "portal" && w.Role != nil && *w.Role == "owner"
}
func validReason(v string) bool {
	if !utf8.ValidString(v) || strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > 500 {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

var gpuPattern = regexp.MustCompile(`^GPU-[a-f0-9-]{36}$`)

func (s *Service) policy() config.TrainingPolicy { return s.Identity.Config.Policy().Training }
func (s *Service) transaction(ctx context.Context, a identity.Auth, workspace string, write bool, fn func(context.Context, pgx.Tx, workspaces.Workspace) error) error {
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
	w, e := s.Workspaces.RecheckScope(ctx, tx, a, workspace)
	if e != nil {
		return e
	}
	if w.Status != "active" || write && !owner(a, w) {
		return fail("ACCESS_DENIED")
	}
	if e = fn(ctx, tx, w); e != nil {
		return e
	}
	if tx.Commit(ctx) != nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

func fingerprint(a identity.Auth, input CreateRequest) []byte {
	raw, _ := json.Marshal(struct {
		Actor    string
		Audience string
		Input    CreateRequest
	}{a.Me.User.ID, a.Me.Audience, input})
	sum := sha256.Sum256(raw)
	return sum[:]
}
func (s *Service) recipe() (ExecutionRecipe, error) {
	c := s.Identity.Config.Training
	if !c.Enabled() || c.Validate(s.Identity.Config.Models.ArtifactRoot, s.Identity.Config.Datasets.ArtifactRoot) != nil || s.Nodes == nil || s.Models == nil || s.Datasets == nil {
		return ExecutionRecipe{}, fail("TRAINING_UNAVAILABLE")
	}
	return ExecutionRecipe{SchemaVersion: 1, WorkerVersion: wire.WorkerVersion, ImageReference: c.ImageReference, Identity: wire.ExecutionIdentity{Profile: wire.ProductionProfile, Python: wire.PythonVersion, WorkerSourceSHA256: c.WorkerSourceSHA256, ImageID: &c.ImageID}, Resources: s.policy().Resources, DataLimits: s.Identity.Config.Policy().Datasets.Limits}, nil
}
func audit(ctx context.Context, tx pgx.Tx, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','training_job',$7,$8,$9)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, id, reason, request)
	return e
}
