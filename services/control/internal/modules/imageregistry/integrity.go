package imageregistry

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// VerifyStored is startup/restore integrity, not remote serving qualification.
// It validates every retained actual archive/blob closure and graph binding in
// bounded rows, never materializes all image bytes or adopts an arbitrary stage.
func (s *Service) VerifyStored(ctx context.Context) error {
	return s.verifyStoredState(ctx, false)
}

// VerifyControlState keeps authenticated explicit recovery reachable while valid
// unfinished pins remain quarantined. It never promotes or resumes an operation.
func (s *Service) VerifyControlState(ctx context.Context) error {
	return s.verifyStoredState(ctx, true)
}

func (s *Service) verifyStoredState(ctx context.Context, controlRecovery bool) error {
	if ctx == nil {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	if err := ctx.Err(); err != nil {
		return safeError(err)
	}
	store, e := s.store()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.OperationTimeout())
	defer cancel()
	return safeError(store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var tenant string
		e := q.QueryRow(ctx, statement(q, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE OF i,t`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`)).Scan(&tenant)
		if e != nil {
			return e
		}
		cursor := ""
		count := 0
		for {
			var query transaction.Statement
			if q.Dialect() == transaction.MySQL {
				query = transaction.SQL(transaction.MySQL, "SELECT "+pinMY+" FROM image_registry_pins WHERE tenant_id=? AND id>? ORDER BY id LIMIT ? FOR SHARE", tenant, cursor, s.config.PageSize)
			} else {
				query = transaction.SQL(transaction.PostgreSQL, "SELECT "+pinPG+" FROM image_registry_pins WHERE tenant_id=$1 AND ($2='' OR id>NULLIF($2,'')::uuid) ORDER BY id LIMIT $3 FOR SHARE", tenant, cursor, s.config.PageSize)
			}
			rows, e := q.Query(ctx, query)
			if e != nil {
				return e
			}
			batch := []Pin{}
			for rows.Next() {
				p, e := scanPin(rows)
				if e != nil {
					rows.Close()
					return e
				}
				batch = append(batch, p)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if len(batch) == 0 {
				break
			}
			for _, p := range batch {
				if p.State != "deleted" {
					count++
				}
				if count > s.config.MaximumPins {
					return failure("REGISTRY_CAPACITY_REQUIRED")
				}
				cursor = p.ID
				var owner, phase string
				if e = q.QueryRow(ctx, statement(q, `SELECT owner_token::text,phase FROM image_registry_imports WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 FOR SHARE`, `SELECT owner_token,phase FROM image_registry_imports WHERE tenant_id=? AND workspace_id=? AND pin_id=? FOR SHARE`, p.tenant, p.WorkspaceID, p.ID)).Scan(&owner, &phase); e != nil {
					return e
				}
				availability, err := storedPinState(p, owner, phase)
				if err != nil {
					return err
				}
				if err = storedBoundMetadata(p, s.config.Verification.MaximumMetadataBytes); err != nil {
					return err
				}
				if availability == storedPinDeleted {
					continue
				}
				if availability == storedPinRecovery {
					if !controlRecovery {
						return failure("REGISTRY_RECOVERY_REQUIRED")
					}
					continue
				}
				image, e := readCached(ctx, s.config, p.ID)
				if e != nil {
					return e
				}
				if e = cachedMatches(p, image); e != nil {
					return e
				}
				raw, _ := json.Marshal(image)
				if p.inventorySHA == nil || *p.inventorySHA != strings.TrimPrefix(digestBytes(raw), "sha256:") {
					return failure("REGISTRY_CONTENT_INVALID")
				}
				descriptorRows, e := q.Query(ctx, statement(q, `SELECT digest,size,media_type,kind FROM image_registry_blobs WHERE tenant_id=$1 AND workspace_id=$2 AND pin_id=$3 ORDER BY ordinal`, `SELECT digest,size,media_type,kind FROM image_registry_blobs WHERE tenant_id=? AND workspace_id=? AND pin_id=? ORDER BY ordinal`, p.tenant, p.WorkspaceID, p.ID))
				if e != nil {
					return e
				}
				i := 0
				for descriptorRows.Next() {
					var d Descriptor
					if e = descriptorRows.Scan(&d.Digest, &d.Size, &d.MediaType, &d.Kind); e != nil {
						descriptorRows.Close()
						return e
					}
					if i >= len(image.Descriptors) || d != image.Descriptors[i] {
						descriptorRows.Close()
						return failure("REGISTRY_STORED_STATE_INVALID")
					}
					i++
				}
				e = descriptorRows.Err()
				descriptorRows.Close()
				if e != nil {
					return e
				}
				if i != len(image.Descriptors) {
					return failure("REGISTRY_STORED_STATE_INVALID")
				}
			}
		}
		var invalid bool
		invalidPin := "p.state<>'ready'"
		if controlRecovery {
			invalidPin = "p.state NOT IN('ready','unknown') OR p.inventory IS NULL"
		}
		var graph transaction.Statement
		if q.Dialect() == transaction.MySQL {
			graph = transaction.SQL(transaction.MySQL, `SELECT EXISTS(SELECT 1 FROM image_registry_deployment_refs r JOIN image_registry_pins p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.pin_id JOIN deployments d ON d.tenant_id=r.tenant_id AND d.workspace_id=r.workspace_id AND d.id=r.deployment_id WHERE r.tenant_id=? AND r.released_at IS NULL AND (`+invalidPin+` OR r.node_id<>d.node_id)) OR EXISTS(SELECT 1 FROM image_registry_offline_refs r JOIN image_registry_pins p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.pin_id WHERE r.tenant_id=? AND r.released_at IS NULL AND (`+invalidPin+`))`, tenant, tenant)
		} else {
			graph = transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS(SELECT 1 FROM image_registry_deployment_refs r JOIN image_registry_pins p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.pin_id JOIN deployments d ON d.tenant_id=r.tenant_id AND d.workspace_id=r.workspace_id AND d.id=r.deployment_id WHERE r.tenant_id=$1 AND r.released_at IS NULL AND (`+invalidPin+` OR r.node_id<>d.node_id)) OR EXISTS(SELECT 1 FROM image_registry_offline_refs r JOIN image_registry_pins p ON p.tenant_id=r.tenant_id AND p.workspace_id=r.workspace_id AND p.id=r.pin_id WHERE r.tenant_id=$1 AND r.released_at IS NULL AND (`+invalidPin+`))`, tenant)
		}
		e = q.QueryRow(ctx, graph).Scan(&invalid)
		if e != nil {
			return e
		}
		if invalid {
			return failure("REGISTRY_STORED_STATE_INVALID")
		}
		return nil
	}))
}
func (s *Service) InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	store, e := s.store()
	if e != nil || q == nil || q.Dialect() != store.Dialect() {
		return failure("REGISTRY_OWNER_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return failure("REGISTRY_TRANSACTION_REQUIRED")
	}
	return InvalidateAuthoritiesWithin(ctx, q)
}

// InvalidateAuthoritiesWithin is the same original registry grant revocation,
// borrowed by coordinated restore even when no live registry transport exists.
// It owns no pool, filesystem, remote connection, commit or physical reference.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL && q.Dialect() != transaction.Dameng {
		return failure("REGISTRY_TRANSACTION_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool {
		return failure("REGISTRY_TRANSACTION_REQUIRED")
	}
	_, e := q.Exec(ctx, vendorStatement(q, `UPDATE image_registry_grants SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL`, `UPDATE image_registry_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE revoked_at IS NULL`, `UPDATE "IMAGE_REGISTRY_GRANTS" SET "REVOKED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "REVOKED_AT" IS NULL`))
	return safeError(e)
}
