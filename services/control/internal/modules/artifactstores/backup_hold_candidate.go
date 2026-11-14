package artifactstores

import (
	"context"
	"os"
	"time"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// The candidate is unmounted until the selected database owner can serialize
// durable writer permits and the provider owner can exclude other writers.
type backupHoldCandidate struct {
	source                           *Service
	store                            transaction.Store
	tenant, namespace, site, backend string
}

type backupHoldBinding struct {
	system                                   string
	databaseOID                              int64
	scope                                    agent.Scope
	tenant, store, siteSHA256, backendSHA256 string
}

type backupHoldToken struct {
	binding          backupHoldBinding
	operation, owner string
	generation       int64
}

type backupHoldCopy struct {
	id, inventory, archive string
	bytes                  int64
}

type backupHoldRow struct {
	token  backupHoldToken
	state  string
	until  *time.Time
	copy   backupHoldCopy
	schema string
}

const backupHoldKey = `system_identifier=$1 AND database_oid=$2::bigint::oid AND tenant_id=$3 AND store_id=$4 AND site_id=$5 AND cell_id=$6`

func (s *Service) backupHoldCandidate(site string) (backupHoldCandidate, error) {
	store, err := s.repository()
	if err != nil {
		return backupHoldCandidate{}, safe(err)
	}
	return backupHoldCandidate{source: s, store: store, tenant: s.Stage.Tenant(), namespace: s.Stage.Namespace(), site: site, backend: s.Stage.Identity()}, nil
}

func (h backupHoldCandidate) withRow(ctx context.Context, fn func(transaction.Executor, backupHoldRow, time.Time) error) error {
	if ctx == nil || h.store == nil || h.store.Dialect() != transaction.PostgreSQL ||
		!id(h.tenant) || !id(h.namespace) || !objectstore.ValidDigest(h.site) || !objectstore.ValidDigest(h.backend) {
		return fail("OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	return safe(h.store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var row backupHoldRow
		binding := &row.token.binding
		err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT h.system_identifier,h.database_oid::bigint,h.site_id,h.cell_id,h.site_sha256,h.backend_sha256,
				h.generation,h.state,COALESCE(h.operation_id::text,''),COALESCE(h.owner_id::text,''),
				h.lease_until,COALESCE(h.copy_id::text,''),COALESCE(h.inventory_sha256,''),
				COALESCE(h.archive_sha256,''),COALESCE(h.archive_bytes,0),n.nspname::text
			FROM pg_catalog.pg_database d
			CROSS JOIN pg_catalog.pg_control_system() c
			CROSS JOIN installation_state i
			JOIN artifact_backup_holds h ON h.tenant_id=i.tenant_id AND h.store_id=$2
			JOIN cells local ON local.is_local AND h.site_id=local.site_id AND h.cell_id=local.id
			JOIN pg_catalog.pg_class rel ON rel.oid='artifact_objects'::regclass
			JOIN pg_catalog.pg_class holds ON holds.oid=h.tableoid AND holds.relnamespace=rel.relnamespace
			JOIN pg_catalog.pg_class topology ON topology.oid=local.tableoid AND topology.relnamespace=rel.relnamespace
			JOIN pg_catalog.pg_namespace n ON n.oid=rel.relnamespace
			WHERE d.datname=pg_catalog.current_database() AND d.oid=h.database_oid
				AND c.system_identifier::text=h.system_identifier AND i.id=1 AND i.bootstrap_complete
				AND i.tenant_id=$1 AND pg_catalog.current_setting('fsync')='on'
			FOR SHARE OF local FOR UPDATE OF h`, h.tenant, h.namespace)).Scan(
			&binding.system, &binding.databaseOID, &binding.scope.SiteID, &binding.scope.CellID,
			&binding.siteSHA256, &binding.backendSHA256, &row.token.generation,
			&row.state, &row.token.operation, &row.token.owner, &row.until, &row.copy.id,
			&row.copy.inventory, &row.copy.archive, &row.copy.bytes, &row.schema)
		if err != nil || !binding.scope.Valid() || binding.siteSHA256 != h.site || binding.backendSHA256 != h.backend {
			return fail("OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
		}
		binding.tenant, binding.store = h.tenant, h.namespace
		if _, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `SET LOCAL synchronous_commit=on`)); err != nil {
			return err
		}
		var now time.Time
		if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT pg_catalog.clock_timestamp()`)).Scan(&now); err != nil {
			return err
		}
		return fn(q, row, now)
	}))
}

func (h backupHoldCandidate) update(ctx context.Context, q transaction.Executor, row backupHoldRow, assignments string, args ...any) error {
	b := row.token.binding
	params := append([]any{b.system, b.databaseOID, b.tenant, b.store, b.scope.SiteID, b.scope.CellID}, args...)
	changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE artifact_backup_holds SET "+assignments+" WHERE "+backupHoldKey, params...))
	if err == nil && changed != 1 {
		return fail("OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	}
	return err
}

// checkWriterGate reports the persisted gate at this instant. Only a
// deployment that independently excludes writers can use it. It is not a permit.
func (h backupHoldCandidate) checkWriterGate(ctx context.Context) error {
	return h.withRow(ctx, func(_ transaction.Executor, row backupHoldRow, _ time.Time) error {
		if row.state != "released" {
			return errBackupFenced
		}
		return nil
	})
}

func (h backupHoldCandidate) acquire(ctx context.Context, operation, owner string, lease time.Duration) (backupHoldToken, error) {
	return h.claim(ctx, backupHoldToken{operation: operation, owner: owner}, "", lease)
}

func (h backupHoldCandidate) takeOver(ctx context.Context, previous backupHoldToken, owner string, lease time.Duration) (backupHoldToken, error) {
	if previous.generation < 1 || !id(owner) || owner == previous.owner {
		return backupHoldToken{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	return h.claim(ctx, previous, owner, lease)
}

func (h backupHoldCandidate) claim(ctx context.Context, prior backupHoldToken, successor string, lease time.Duration) (backupHoldToken, error) {
	// PostgreSQL timestamps have microsecond resolution. Bound the coordinator
	// policy even if its input could otherwise overflow the database interval.
	if !id(prior.operation) || !id(prior.owner) || successor != "" && !id(successor) ||
		lease < time.Millisecond || lease > 24*time.Hour {
		return backupHoldToken{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	var token backupHoldToken
	err := h.withRow(ctx, func(q transaction.Executor, row backupHoldRow, now time.Time) error {
		owner := prior.owner
		if successor != "" {
			if row.token.binding != prior.binding {
				return fail("OBJECT_BACKUP_TOKEN_INVALID")
			}
			owner = successor
		}
		if row.state != "released" && row.token.operation == prior.operation && row.token.owner == owner &&
			now.Before(*row.until) && (successor == "" || row.token.generation-1 == prior.generation) {
			token = row.token
			return nil
		}
		if successor != "" {
			if row.state == "released" || row.token != prior || now.Before(*row.until) {
				return fail("OBJECT_BACKUP_TOKEN_INVALID")
			}
		} else if row.state != "released" || row.token.operation == prior.operation {
			return errBackupFenced
		}
		token = backupHoldToken{binding: row.token.binding, operation: prior.operation, owner: owner, generation: row.token.generation + 1}
		return h.update(ctx, q, row, `state='held', generation=generation+1, operation_id=$7, owner_id=$8,
			lease_until=$9, copy_id=NULL, inventory_sha256=NULL, archive_sha256=NULL, archive_bytes=NULL`,
			token.operation, token.owner, now.Add(lease))
	})
	if err != nil {
		return backupHoldToken{}, err
	}
	return token, nil
}

func (r backupHoldRow) active(token backupHoldToken, now time.Time) bool {
	return r.state != "released" && r.token == token && now.Before(*r.until)
}

func (h backupHoldCandidate) copyAndConfirm(ctx context.Context, token backupHoldToken, copyID string, f *BackupFence, dst *os.File, objects int, bytes int64) (backupHoldCopy, error) {
	if !id(copyID) || h.source == nil || f == nil || f.service != h.source {
		return backupHoldCopy{}, fail("OBJECT_BACKUP_REQUEST_INVALID")
	}
	if err := h.withRow(ctx, func(_ transaction.Executor, row backupHoldRow, now time.Time) error {
		if !row.active(token, now) || row.state != "held" {
			return fail("OBJECT_BACKUP_TOKEN_INVALID")
		}
		return nil
	}); err != nil {
		return backupHoldCopy{}, err
	}
	copy, err := f.CopyInventory(ctx, dst, objects, bytes)
	if err != nil {
		return backupHoldCopy{}, err
	}

	// Prevent a local release between archive readback and the SQL decision.
	l := f.service.lifecycle
	l.mu.Lock()
	defer l.mu.Unlock()
	if !f.service.admittedLocked() || l.backup != f {
		return backupHoldCopy{}, fail("OBJECT_BACKUP_TOKEN_INVALID")
	}
	return h.confirm(ctx, token, copyID, copy)
}

func (h backupHoldCandidate) confirm(ctx context.Context, token backupHoldToken, copyID string, copy BackupCopy) (backupHoldCopy, error) {
	receipt := backupHoldCopy{copyID, copy.Inventory.InventorySHA256, copy.SHA256, copy.SizeBytes}
	err := h.withRow(ctx, func(q transaction.Executor, row backupHoldRow, now time.Time) error {
		if !row.active(token, now) || row.state != "held" {
			return fail("OBJECT_BACKUP_TOKEN_INVALID")
		}
		i := copy.Inventory
		if i.DatabaseOID != row.token.binding.databaseOID || i.scope != row.token.binding.scope ||
			i.Schema != row.schema || i.TenantID != h.tenant ||
			i.StoreID != h.namespace || i.BackendSHA256 != h.backend {
			return fail("OBJECT_BACKUP_SCOPE_MISMATCH")
		}
		return h.update(ctx, q, row, `state='confirmed', copy_id=$7, inventory_sha256=$8, archive_sha256=$9, archive_bytes=$10`,
			receipt.id, receipt.inventory, receipt.archive, receipt.bytes)
	})
	if err != nil {
		return backupHoldCopy{}, err
	}
	return receipt, nil
}

func (h backupHoldCandidate) release(ctx context.Context, token backupHoldToken, receipt backupHoldCopy) error {
	return h.withRow(ctx, func(q transaction.Executor, row backupHoldRow, now time.Time) error {
		if row.token != token || token.generation < 1 || row.state != "released" && !row.active(token, now) {
			return fail("OBJECT_BACKUP_TOKEN_INVALID")
		}
		if row.state == "held" || row.copy != receipt {
			return fail("OBJECT_BACKUP_COPY_UNCONFIRMED")
		}
		if row.state == "released" {
			return nil
		}
		return h.update(ctx, q, row, `state='released', lease_until=NULL`)
	})
}
