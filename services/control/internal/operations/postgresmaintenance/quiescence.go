// Package postgresmaintenance owns same-session installation-fenced PostgreSQL
// maintenance observations. It never creates a pool, changes domain state or
// converts a SQL observation into physical workload-removal evidence.
package postgresmaintenance

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const ApplicationProfile = "releases"
const SchemaVersion = 22

// Each predicate names a current cumulative owner and keeps uncertain authority
// blocking. Terminal accepted history and pending human review remain retained.
const remoteWorkSQL = `SELECT
 EXISTS(SELECT 1 FROM public.node_tasks WHERE state NOT IN ('succeeded','failed','cancelled')),
 EXISTS(SELECT 1 FROM public.managed_components WHERE desired_state='running' OR observed_state NOT IN ('stopped','not_installed')),
 EXISTS(SELECT 1 FROM public.gpu_leases WHERE state<>'released'),
 EXISTS(SELECT 1 FROM public.deployments WHERE desired_state='running' OR observed_state NOT IN ('stopped','not_installed')),
 EXISTS(SELECT 1 FROM public.gateway_requests WHERE capacity_held OR state='admitted'),
 EXISTS(SELECT 1 FROM public.training_jobs WHERE state NOT IN ('succeeded','failed','cancelled')),
 EXISTS(SELECT 1 FROM public.training_attempts WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping','recovery_required')),
 EXISTS(SELECT 1 FROM public.training_uploads WHERE state NOT IN ('accepted','failed')),
 EXISTS(SELECT 1 FROM public.model_preparations WHERE state NOT IN ('quality_failed','awaiting_review','approved','rejected','failed','cancelled')),
 EXISTS(SELECT 1 FROM public.model_preparation_attempts WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping','recovery_required')),
 EXISTS(SELECT 1 FROM public.model_preparation_uploads WHERE state NOT IN ('accepted','failed')),
 EXISTS(SELECT 1 FROM public.workflow_runs WHERE state IN ('pending','running') OR lease_id IS NOT NULL),
 EXISTS(SELECT 1 FROM public.workflow_node_traces WHERE state='running'),
 EXISTS(SELECT 1 FROM public.model_imports WHERE state NOT IN ('ready','cancelled'))`

var checkKinds = [...]string{"NODE_TASKS", "COMPONENTS", "GPU_LEASES", "INFERENCE", "GATEWAY_REQUESTS", "TRAINING_JOBS", "TRAINING_ATTEMPTS", "TRAINING_UPLOADS", "PREPARATIONS", "PREPARATION_ATTEMPTS", "PREPARATION_UPLOADS", "WORKFLOWS", "WORKFLOW_TRACES", "MODEL_IMPORTS"}

const heldReadOnlySQL = `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_catalog.pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted),
 pg_catalog.current_setting('transaction_read_only')='on' AND pg_catalog.current_schemas(true)=ARRAY['pg_catalog','public']::name[]`

const installationSQL = `SELECT pg_catalog.pg_backend_pid(),pg_catalog.current_database(),CURRENT_USER,i.tenant_id::text,pg_catalog.clock_timestamp(),
 (SELECT COUNT(*) FROM public.installation_state)=1
 FROM public.installation_state i
 JOIN public.tenants t ON t.id=i.tenant_id AND t.status='active'
 JOIN public.workspaces w ON w.tenant_id=i.tenant_id AND w.id=i.default_workspace_id
 JOIN public.users u ON u.id=i.primary_admin_user_id AND u.is_platform_admin AND u.status='active'
 WHERE i.id=1 AND i.bootstrap_complete AND i.bootstrapped_at IS NOT NULL`

type Observation struct {
	FormatVersion      int      `json:"format_version"`
	Status             string   `json:"status"`
	ApplicationProfile string   `json:"application_profile"`
	SchemaVersion      int      `json:"schema_version"`
	Database           string   `json:"database"`
	Operator           string   `json:"operator"`
	TenantID           string   `json:"tenant_id"`
	BackendPID         int      `json:"backend_pid"`
	ObservedAt         string   `json:"observed_at"`
	Checks             []string `json:"checks"`
	CurrentLockHeld    bool     `json:"current_lock_held"`
	Qualification      string   `json:"qualification"`
}

func checkHeldReadOnly(ctx context.Context, q transaction.Executor) error {
	var held, readOnly bool
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, heldReadOnlySQL, database.InstallationLockID)).Scan(&held, &readOnly) != nil || !held || !readOnly {
		return errors.New("POSTGRES_MAINTENANCE_LOCKED_READ_ONLY_SESSION_REQUIRED")
	}
	return nil
}

func noActiveWork(active [len(checkKinds)]bool) error {
	for i, blocked := range active {
		if blocked {
			return errors.New("POSTGRES_MAINTENANCE_" + checkKinds[i] + "_NOT_QUIESCENT")
		}
	}
	return nil
}

// checkRemoteWorkWithin stays private: only CheckWithin exposes the complete
// schema/installation/session gate. SQL predicate fixtures exercise this reader
// separately and never establish a complete installed-profile observation.
func checkRemoteWorkWithin(ctx context.Context, q transaction.Executor) error {
	var active [len(checkKinds)]bool
	destinations := make([]any, len(active))
	for i := range active {
		destinations[i] = &active[i]
	}
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, remoteWorkSQL)).Scan(destinations...) != nil {
		return errors.New("POSTGRES_MAINTENANCE_REMOTE_STATE_UNAVAILABLE")
	}
	return noActiveWork(active)
}

// CheckWithin borrows an explicitly read-only transaction on the selected
// installation-lock session. The caller owns its transaction and lock lifetime.
// It neither commits nor releases either. A later backup must recheck while its
// own continuous lock is held; this DTO cannot authorize that later operation.
func CheckWithin(ctx context.Context, q transaction.Executor, profile string) (Observation, error) {
	refuse := func(code string) (Observation, error) { return Observation{}, errors.New(code) }
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL || !supportedProfile(profile) {
		return refuse("POSTGRES_MAINTENANCE_RELEASES_CONTEXT_REQUIRED")
	}
	if e := checkHeldReadOnly(ctx, q); e != nil {
		return Observation{}, e
	}
	if e := database.CheckAccountProfileWithin(ctx, q, profile); e != nil {
		return refuse("POSTGRES_MAINTENANCE_SCHEMA_INCOMPATIBLE")
	}
	var out Observation
	var at time.Time
	var single bool
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, installationSQL)).Scan(&out.BackendPID, &out.Database, &out.Operator, &out.TenantID, &at, &single) != nil {
		return refuse("POSTGRES_MAINTENANCE_INSTALLATION_INVALID")
	}
	tenant, e := uuid.Parse(out.TenantID)
	if e != nil || tenant.String() != out.TenantID || !single || out.BackendPID < 1 || out.Database == "" || out.Operator == "" || at.IsZero() {
		return refuse("POSTGRES_MAINTENANCE_INSTALLATION_INVALID")
	}
	if e = checkRemoteWorkWithin(ctx, q); e != nil {
		return Observation{}, e
	}
	if profile == MVPApplicationProfile {
		if e = checkMVPRemoteWorkWithin(ctx, q); e != nil {
			return Observation{}, e
		}
	}
	if e = checkHeldReadOnly(ctx, q); e != nil {
		return Observation{}, e
	}
	out.FormatVersion = 1
	out.Status = "observed_quiescent_under_installation_lock"
	out.ApplicationProfile = profile
	out.SchemaVersion, e = checkedSchemaVersion(ctx, q, profile)
	if e != nil {
		return Observation{}, e
	}
	out.ObservedAt = at.UTC().Format(time.RFC3339Nano)
	out.CurrentLockHeld = true
	out.Checks = append([]string{}, checkKinds[:]...)
	if profile == MVPApplicationProfile {
		out.Checks = append(out.Checks, mvpCheckKinds[:]...)
	}
	out.Qualification = "SQL-declared workload and capacity quiescence during this exact locked read-only transaction only. No physical removal, file consistency, backup, later mutation authority or target qualification."
	return out, nil
}
