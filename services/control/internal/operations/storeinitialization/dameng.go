//go:build cgo && odbc_manager

package storeinitialization

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func DamengSupported(command string) bool {
	return Supported(command) || command == ConfiguredModelCommand || command == CheckModelCommand || command == RecordModelCommand
}

func damengDefinition(c config.Config, command string) (definition, error) {
	if command == ConfiguredModelCommand || command == CheckModelCommand || command == RecordModelCommand {
		mode := initialize
		if command == CheckModelCommand {
			mode = check
		}
		if command == RecordModelCommand {
			mode = record
		}
		return definition{kind: "model", mode: mode, root: c.Models.ArtifactRoot, action: "model.store_initialize_verified", target: "model_store", history: `SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_VERSIONS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "MODEL_IMPORTS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "MODEL_IMPORT_CHUNKS" c JOIN "MODEL_IMPORTS" i ON i."ID"=c."IMPORT_ID" WHERE i."TENANT_ID"=?) THEN 1 ELSE 0 END FROM DUAL`}, nil
	}
	d, e := describe(c, command)
	if e != nil {
		return definition{}, e
	}
	switch d.kind {
	case "dataset":
		d.history = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "DATASETS" WHERE "TENANT_ID"=?) THEN 1 ELSE 0 END FROM DUAL`
	case "training":
		d.history = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "TRAINING_JOBS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "TRAINING_UPLOADS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "TRAINING_CHECKPOINTS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "TRAINING_ARTIFACTS" WHERE "TENANT_ID"=?) THEN 1 ELSE 0 END FROM DUAL`
	case "preparation":
		d.history = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "MODEL_PREPARATION_UPLOADS" WHERE "TENANT_ID"=?) OR EXISTS(SELECT 1 FROM "MODEL_PREPARATION_ARTIFACTS" WHERE "TENANT_ID"=?) THEN 1 ELSE 0 END FROM DUAL`
	}

	return d, nil
}

func damengTenant(ctx context.Context, q transaction.Executor, lock bool) (string, error) {
	if q == nil || q.Dialect() != transaction.Dameng {
		return "", errors.New("STORE_INITIALIZATION_CONTEXT_INVALID")
	}
	query := `SELECT "TENANT_ID","DEFAULT_WORKSPACE_ID","PRIMARY_ADMIN_USER_ID","BOOTSTRAP_COMPLETE","BOOTSTRAPPED_AT",(SELECT COUNT(*) FROM "INSTALLATION_STATE") FROM "INSTALLATION_STATE" WHERE "ID"=1`
	if lock {
		query += " FOR UPDATE"
	}
	var tenant, workspace, admin sql.NullString
	var at sql.NullTime
	var complete bool
	var single int
	if q.QueryRow(ctx, transaction.SQL(transaction.Dameng, query)).Scan(&tenant, &workspace, &admin, &complete, &at, &single) != nil || !complete || single != 1 || !at.Valid || !tenant.Valid || !workspace.Valid || !admin.Valid {
		return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
	}
	for _, id := range []string{tenant.String, workspace.String, admin.String} {
		parsed, e := uuid.Parse(id)
		if e != nil || parsed == uuid.Nil || parsed.String() != id {
			return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
		}
	}
	graph := `SELECT i."TENANT_ID" FROM "INSTALLATION_STATE" i JOIN "TENANTS" t ON t."ID"=i."TENANT_ID" JOIN "WORKSPACES" w ON w."TENANT_ID"=t."ID" AND w."ID"=i."DEFAULT_WORKSPACE_ID" JOIN "USERS" u ON u."ID"=i."PRIMARY_ADMIN_USER_ID" JOIN "WORKSPACE_MEMBERSHIPS" m ON m."TENANT_ID"=t."ID" AND m."WORKSPACE_ID"=w."ID" AND m."USER_ID"=u."ID" WHERE i."ID"=1 AND i."BOOTSTRAP_COMPLETE"=1 AND t."STATUS"='active' AND w."STATUS"='active' AND u."STATUS"='active' AND u."IS_PLATFORM_ADMIN"=1 AND m."ROLE"='owner'`
	if lock {
		graph += " FOR UPDATE"
	}
	var actual string
	if q.QueryRow(ctx, transaction.SQL(transaction.Dameng, graph)).Scan(&actual) != nil || actual != tenant.String {
		return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
	}
	return actual, nil
}

func damengNoHistory(ctx context.Context, q transaction.Executor, d definition, tenant string) error {
	args := []any{tenant}
	switch d.kind {
	case "training":
		args = append(args, tenant, tenant, tenant)
	case "preparation", "model":
		args = append(args, tenant, tenant)
	}
	var retained bool
	if q.QueryRow(ctx, transaction.SQL(transaction.Dameng, d.history, args...)).Scan(&retained) != nil {
		return errors.New("STORE_INITIALIZATION_HISTORY_UNAVAILABLE")
	}
	if retained {
		return errors.New("STORE_INITIALIZATION_RESTORE_REQUIRED")
	}
	return nil
}

// RunDameng borrows the actual selected installation owner. It never substitutes
// a PostgreSQL transaction, creates missing roots or recreates retained stores.
func RunDameng(ctx context.Context, operator *databaseaccess.DamengContext, c config.Config, o Options) (string, error) {
	d, e := damengDefinition(c, o.Command)
	if e != nil {
		return "", e
	}
	if d.root == "" || o.Root != d.root || o.UID <= 0 || o.GID <= 0 {
		return "", errors.New("STORE_INITIALIZATION_CONFIGURED_ROOT_REQUIRED")
	}
	if d.mode == initialize && !o.ConfirmEmpty {
		return "", errors.New("STORE_INITIALIZATION_EMPTY_CONFIRMATION_REQUIRED")
	}
	if d.kind == "model" && d.mode == initialize {
		// This command preserves its original exact flags, which have no reason.
		// The action itself is the structural reason, never a default user reason.
		o.Reason = ConfiguredModelCommand
	} else if d.mode != check && !modelpreparations.ValidOperatorReason(o.Reason) {
		return "", errors.New("OPERATOR_REASON_REQUIRED")
	}
	if e = operator.VerifyConfiguration(ctx, c); e != nil {
		return "", e
	}
	if e = operator.VerifyInstallation(ctx); e != nil {
		return "", e
	}
	store, e := operator.Store(ctx)
	if e != nil {
		return "", e
	}
	tenant, e := damengTenant(ctx, store, false)
	if e != nil {
		return "", e
	}
	if e = damengNoHistory(ctx, store, d, tenant); e != nil {
		return "", e
	}
	status := ""
	recordProof := func(proof string, recheck func() error) error {
		metadata := map[string]any{"runtime_uid": o.UID, "runtime_gid": o.GID, "source": "protected_local_operator", "referenced_files_verified": true, "configured_root_sha256": damengRootDigest([]byte(o.Root))}
		target := proof
		if d.kind == "model" {
			metadata["proof_kind"], metadata["directory_identity_sha256"] = "empty_directory_identity", proof
			target = uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenant+"\x00"+proof)).String()
		} else {
			metadata["proof_kind"], metadata["store_id"] = "namespace_uuid", proof
		}
		raw, e := json.Marshal(metadata)
		if e != nil {
			return errors.New("STORE_INITIALIZATION_PROOF_INVALID")
		}
		if e = operator.VerifyConfiguration(ctx, c); e != nil {
			return e
		}
		if e = recheck(); e != nil {
			return e
		}
		e = store.Within(ctx, d.mode == check, func(ctx context.Context, q transaction.Executor) error {
			actual, e := damengTenant(ctx, q, d.mode != check)
			if e != nil || actual != tenant {
				return errors.New("STORE_INITIALIZATION_INSTALLATION_CHANGED")
			}
			if e = damengNoHistory(ctx, q, d, tenant); e != nil {
				return e
			}
			if e = recheck(); e != nil {
				return e
			}
			exists, e := damengAuditProof(ctx, q, d, tenant, target, metadata)
			if e != nil {
				return e
			}
			if exists {
				status = Verified
				return nil
			}

			if d.mode == check {
				status = NotRecorded
				return nil
			}
			changed, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "AUDIT_EVENTS" ("ID","OCCURRED_AT","TENANT_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID","METADATA") VALUES(?,CURRENT_TIMESTAMP(6),?,'operator',?,'success',?,?,?,?,?)`, uuid.NewString(), tenant, d.action, d.target, target, o.Reason, uuid.NewString(), string(raw)))
			if e != nil {
				return e
			}
			if changed != 1 {
				return errors.New("STORE_INITIALIZATION_AUDIT_WRITE_UNCERTAIN")
			}

			status = Verified
			return recheck()
		})
		if e != nil {
			return e
		}
		if e = recheck(); e != nil {
			return e
		}
		return operator.VerifyConfiguration(ctx, c)
	}
	if d.kind == "model" {
		e = models.WithEmptyStoreIdentity(o.Root, o.UID, o.GID, d.mode == initialize, recordProof)
	} else {
		if d.mode == initialize {
			switch d.kind {
			case "dataset":
				e = datasets.InitializeStore(o.Root, tenant, o.UID, o.GID)
			case "training":
				e = training.InitializeStore(o.Root, tenant, o.UID, o.GID)
			case "preparation":
				e = modelpreparations.InitializeStore(o.Root, tenant, o.UID, o.GID)
			}
			if e != nil {
				return "", e
			}
		}
		e = withNamespaceLock(o, func(recheck func() error) error {
			proof, e := namespaceProof(d, o, tenant)
			if e != nil {
				return e
			}
			return recordProof(proof, func() error {
				if e := recheck(); e != nil {
					return e
				}
				actual, e := namespaceProof(d, o, tenant)
				if e != nil || actual != proof {
					return errors.New("STORE_INITIALIZATION_PROOF_CHANGED")
				}
				return nil
			})
		})
	}
	if e != nil {
		return "", e
	}
	return status, nil
}

func damengRootDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// Exact owned provenance, bounded to two rows so duplicate records fail closed.
// JSON is validated text; no bytea/RawMessage parameter or vendor JSON rewrite.
func damengAuditProof(ctx context.Context, q transaction.Executor, d definition, tenant, target string, metadata map[string]any) (bool, error) {
	rows, e := q.Query(ctx, transaction.SQL(transaction.Dameng, `SELECT "OUTCOME","AUDIENCE","TARGET_TYPE","TARGET_ID","METADATA" FROM "AUDIT_EVENTS" WHERE "TENANT_ID"=? AND "ACTION"=? LIMIT 2`, tenant, d.action))
	if e != nil {
		return false, errors.New("STORE_INITIALIZATION_AUDIT_UNAVAILABLE")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var outcome, audience, kind, id, raw string
		if e = rows.Scan(&outcome, &audience, &kind, &id, &raw); e != nil {
			return false, errors.New("STORE_INITIALIZATION_AUDIT_UNAVAILABLE")
		}
		var actual map[string]any
		if count != 1 || outcome != "success" || audience != "operator" || kind != d.target || id != target || len(raw) > 4096 || wire.StrictJSON([]byte(raw), &actual) != nil {
			return false, errors.New("STORE_INITIALIZATION_AUDIT_CONFLICT")
		}
		a, ae := json.Marshal(actual)
		b, be := json.Marshal(metadata)
		if ae != nil || be != nil || !bytes.Equal(a, b) {
			return false, errors.New("STORE_INITIALIZATION_AUDIT_CONFLICT")
		}
	}
	if rows.Err() != nil {
		return false, errors.New("STORE_INITIALIZATION_AUDIT_UNAVAILABLE")
	}
	return count == 1, nil
}
