package deployments

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

func fail(code string) error { return errors.New(code) }
func leaseError(e error) error {
	var value *pgconn.PgError
	if errors.As(e, &value) && (value.ConstraintName == "gpu_exclusive_uuid_idx" || value.ConstraintName == "gpu_exclusive_node_idx") {
		return fail("INFERENCE_RESOURCE_BUSY")
	}
	return e
}
func id(value string) bool {
	parsed, e := uuid.Parse(value)
	return e == nil && parsed.String() == value
}
func validReason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 500
}
func fingerprint(actor string, input any) []byte {
	raw, _ := json.Marshal(struct {
		Actor string
		Input any
	}{actor, input})
	sum := sha256.Sum256(raw)
	return sum[:]
}

func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Deployment, error) {
	return s.createSelected(ctx, a, input, key, request)
}
func (s *Service) Action(ctx context.Context, a identity.Auth, deployment string, input ActionRequest, key, request string) (Deployment, error) {
	return s.actionSelected(ctx, a, deployment, input, key, request)
}
func (s *Service) Discover(ctx context.Context, a identity.Auth, node, reason, key, request string) (nodes.Task, error) {
	return s.discoverSelected(ctx, a, node, reason, key, request)
}

const fieldsTemplate = "d.id::text,d.workspace_id::text,d.node_id::text,d.model_id::text,d.gpu_uuid,d.desired_state,d.observed_state,d.revision,d.observation,d.observed_at,d.last_task_id::text,t.state,t.error_code,t.deadline_at,g.state,(n.status<>'active' OR n.last_heartbeat_at<clock_timestamp()-make_interval(secs=>%d) OR d.observed_at IS NULL OR COALESCE((d.observation->>'revision')::bigint,0)<>d.revision OR d.observed_at<clock_timestamp()-make_interval(secs=>%d)),d.created_at,d.removed_at,d.model_kind,d.catalog_digest,d.serving_recipe_sha256"
const joins = " FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id JOIN managed_nodes n ON n.id=d.node_id LEFT JOIN node_tasks t ON t.id=d.last_task_id"

func scan(row pgx.Row) (Deployment, error) {
	var d Deployment
	var raw []byte
	e := row.Scan(&d.ID, &d.WorkspaceID, &d.NodeID, &d.ModelID, &d.GPUUUID, &d.DesiredState, &d.ObservedState, &d.Revision, &raw, &d.ObservedAt, &d.LastTaskID, &d.TaskState, &d.TaskErrorCode, &d.TaskDeadlineAt, &d.ResourceState, &d.Stale, &d.CreatedAt, &d.RemovedAt, &d.ModelKind, &d.CatalogDigest, &d.ServingRecipeSHA256)
	if errors.Is(e, pgx.ErrNoRows) {
		return Deployment{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return Deployment{}, e
	}
	if raw != nil {
		var o wire.DeploymentObservation
		if json.Unmarshal(raw, &o) != nil {
			return Deployment{}, fail("STORAGE_UNAVAILABLE")
		}
		if d.ObservedAt == nil || (wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: &o, ObservedAt: *d.ObservedAt}).Validate() != nil ||
			o.DeploymentID != d.ID || o.ModelID != d.ModelID || o.GPUUUID != d.GPUUUID || o.Revision > d.Revision || !matchesStoredServing(d, o) {
			return Deployment{}, fail("STORAGE_UNAVAILABLE")
		}
		d.Observation = &o
	}
	d.CreatedAt = d.CreatedAt.UTC()
	for _, stamp := range []*time.Time{d.ObservedAt, d.TaskDeadlineAt, d.RemovedAt} {
		if stamp != nil {
			*stamp = stamp.UTC()
		}
	}
	return d, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, deployment string) (Deployment, error) {
	return s.getSelected(ctx, a, workspace, deployment)
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace string) ([]Deployment, error) {
	return s.ListFiltered(ctx, a, workspace, "", false)
}
func (s *Service) ListFiltered(ctx context.Context, a identity.Auth, workspace, model string, retained bool) ([]Deployment, error) {
	return s.listSelected(ctx, a, workspace, model, retained)
}
func (s *Service) Node(ctx context.Context, a identity.Auth, workspace, node string) (NodeState, error) {
	return s.nodeSelected(ctx, a, workspace, node)
}

func (s *Service) fields() string {
	age := s.Nodes.Identity.Config.Policy().Nodes.OnlineSeconds
	return fmt.Sprintf(fieldsTemplate, age, age)
}
