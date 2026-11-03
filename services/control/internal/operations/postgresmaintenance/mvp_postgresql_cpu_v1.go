package postgresmaintenance

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This is a distinct forward lineage. ApplicationProfile and SchemaVersion
// retain their original releases/account22 meaning.
const MVPApplicationProfile = "mvp-postgresql-cpu-v1"

func supportedProfile(profile string) bool {
	return profile == ApplicationProfile || profile == MVPApplicationProfile
}

// Called only after the exact profile ledger/catalog owner has admitted the
// borrowed transaction. The observed version cannot admit a foreign lineage.
func checkedSchemaVersion(ctx context.Context, q transaction.Executor, profile string) (int, error) {
	if profile == ApplicationProfile {
		return SchemaVersion, nil
	}
	var version, count int
	if profile != MVPApplicationProfile || q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT COALESCE(max(version),0),count(*) FROM public.schema_migrations`)).Scan(&version, &count) != nil || version <= SchemaVersion || version != count {
		return 0, errors.New("POSTGRES_MAINTENANCE_SCHEMA_INCOMPATIBLE")
	}
	return version, nil
}

// No age-based release is inferred. Unresolved external mutations, deliveries
// and physical cluster custody must be reconciled by their original owners.
// AWS receipts are completed immutable observations, never pending work.
const mvpRemoteWorkSQL = `SELECT
 EXISTS(SELECT 1 FROM public.mongodb_dataset_imports WHERE state IN ('reading','exported') OR lease_expires_at IS NOT NULL),
 EXISTS(SELECT 1 FROM public.artifact_objects WHERE operation_id IS NOT NULL OR state NOT IN ('ready','deleted','aborted')),
 EXISTS(SELECT 1 FROM public.artifact_object_ingestions WHERE state IN ('intent','running')),
 EXISTS(SELECT 1 FROM public.image_registry_pins WHERE state IN ('importing','unknown','deleting')),
 EXISTS(SELECT 1 FROM public.image_registry_imports WHERE phase IN ('prepared','remote','unknown')),
 EXISTS(SELECT 1 FROM public.task_event_outbox WHERE claim_id IS NOT NULL OR outcome_unknown OR state IN ('prepared','sent','published')),
 EXISTS(SELECT 1 FROM public.middleware_operations WHERE state IN ('queued','running') OR lease_expires_at IS NOT NULL),
 EXISTS(SELECT 1 FROM public.private_cluster_connections WHERE active_run_id IS NOT NULL)
  OR EXISTS(SELECT 1 FROM public.private_cluster_runs WHERE phase<>'cleaned')`

var mvpCheckKinds = [...]string{"MONGODB_IMPORTS", "ARTIFACT_OBJECTS", "ARTIFACT_INGESTIONS", "IMAGE_REGISTRY", "IMAGE_REGISTRY_IMPORTS", "TASK_EVENTS", "MIDDLEWARE", "PRIVATE_CLUSTERS"}

func noMVPActiveWork(active [len(mvpCheckKinds)]bool) error {
	for i, blocked := range active {
		if blocked {
			return errors.New("POSTGRES_MAINTENANCE_" + mvpCheckKinds[i] + "_NOT_QUIESCENT")
		}
	}
	return nil
}

func checkMVPRemoteWorkWithin(ctx context.Context, q transaction.Executor) error {
	var active [len(mvpCheckKinds)]bool
	destinations := make([]any, len(active))
	for i := range active {
		destinations[i] = &active[i]
	}
	if q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, mvpRemoteWorkSQL)).Scan(destinations...) != nil {
		return errors.New("POSTGRES_MAINTENANCE_MVP_REMOTE_STATE_UNAVAILABLE")
	}
	return noMVPActiveWork(active)
}
