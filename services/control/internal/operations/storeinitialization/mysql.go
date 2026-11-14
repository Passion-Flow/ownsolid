package storeinitialization

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const ConfiguredModelCommand = "initialize-configured-model-store"
const CheckModelCommand = "check-configured-model-store-initialization"
const RecordModelCommand = "record-configured-model-store-initialization"

func MySQLSupported(command string) bool {
	return Supported(command) || command == ConfiguredModelCommand || command == CheckModelCommand || command == RecordModelCommand
}

func mysqlDefinition(c config.Config, command string) (definition, error) {
	if command == ConfiguredModelCommand || command == CheckModelCommand || command == RecordModelCommand {
		mode := initialize
		if command == CheckModelCommand {
			mode = check
		}
		if command == RecordModelCommand {
			mode = record
		}
		return definition{kind: "model", mode: mode, root: c.Models.ArtifactRoot, action: "model.store_initialize_verified", target: "model_store", history: `SELECT EXISTS(SELECT 1 FROM model_versions WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM model_imports WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM model_import_chunks c JOIN model_imports i ON i.id=c.import_id WHERE i.tenant_id=?)`}, nil
	}
	d, e := describe(c, command)
	if e != nil {
		return definition{}, e
	}
	switch d.kind {
	case "dataset":
		d.history = `SELECT EXISTS(SELECT 1 FROM datasets WHERE tenant_id=?)`
	case "training":
		d.history = `SELECT EXISTS(SELECT 1 FROM training_jobs WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM training_uploads WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM training_checkpoints WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM training_artifacts WHERE tenant_id=?)`
	case "preparation":
		d.history = `SELECT EXISTS(SELECT 1 FROM model_preparations WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM model_preparation_uploads WHERE tenant_id=?) OR EXISTS(SELECT 1 FROM model_preparation_artifacts WHERE tenant_id=?)`
	}
	return d, nil
}

func mysqlTenant(ctx context.Context, q transaction.Executor, lock bool) (string, error) {
	if q == nil || q.Dialect() != transaction.MySQL {
		return "", errors.New("STORE_INITIALIZATION_CONTEXT_INVALID")
	}
	query := `SELECT tenant_id,default_workspace_id,primary_admin_user_id,bootstrap_complete,bootstrapped_at,(SELECT COUNT(*) FROM installation_state)=1 FROM installation_state WHERE id=1`
	if lock {
		query += " FOR UPDATE"
	}
	var tenant, workspace, admin sql.NullString
	var at sql.NullTime
	var complete, single bool
	if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, query)).Scan(&tenant, &workspace, &admin, &complete, &at, &single) != nil || !complete || !single || !at.Valid || !tenant.Valid || !workspace.Valid || !admin.Valid {
		return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
	}
	for _, id := range []string{tenant.String, workspace.String, admin.String} {
		parsed, e := uuid.Parse(id)
		if e != nil || parsed == uuid.Nil || parsed.String() != id {
			return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
		}
	}
	graph := `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.tenant_id=t.id AND w.id=i.default_workspace_id JOIN users u ON u.id=i.primary_admin_user_id JOIN workspace_memberships m ON m.tenant_id=t.id AND m.workspace_id=w.id AND m.user_id=u.id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' AND w.status='active' AND u.status='active' AND u.is_platform_admin=1 AND m.role='owner'`
	if lock {
		graph += " FOR SHARE"
	}
	var actual string
	if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, graph)).Scan(&actual) != nil || actual != tenant.String {
		return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
	}
	return actual, nil
}

func mysqlNoHistory(ctx context.Context, q transaction.Executor, d definition, tenant string) error {
	args := []any{tenant}
	switch d.kind {
	case "training":
		args = append(args, tenant, tenant, tenant)
	case "preparation", "model":
		args = append(args, tenant, tenant)
	}
	var retained bool
	if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, d.history, args...)).Scan(&retained) != nil {
		return errors.New("STORE_INITIALIZATION_HISTORY_UNAVAILABLE")
	}
	if retained {
		return errors.New("STORE_INITIALIZATION_RESTORE_REQUIRED")
	}
	return nil
}

func namespaceProof(d definition, o Options, tenant string) (string, error) {
	switch d.kind {
	case "dataset":
		return datasets.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	case "training":
		return training.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	case "preparation":
		return modelpreparations.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	}
	return "", errors.New("STORE_INITIALIZATION_COMMAND_INVALID")
}

// RunMySQL borrows the actual selected installation owner. It never substitutes
// a PostgreSQL transaction, creates missing roots or recreates retained stores.
func RunMySQL(ctx context.Context, operator *mysqloperator.Context, c config.Config, o Options) (string, error) {
	d, e := mysqlDefinition(c, o.Command)
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
	tenant, e := mysqlTenant(ctx, store, false)
	if e != nil {
		return "", e
	}
	if e = mysqlNoHistory(ctx, store, d, tenant); e != nil {
		return "", e
	}
	status := ""
	recordProof := func(proof string, recheck func() error) error {
		metadata := map[string]any{"runtime_uid": o.UID, "runtime_gid": o.GID, "source": "protected_local_operator", "referenced_files_verified": true, "configured_root_sha256": mysqloperator.SHA256([]byte(o.Root))}
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
			actual, e := mysqlTenant(ctx, q, d.mode != check)
			if e != nil || actual != tenant {
				return errors.New("STORE_INITIALIZATION_INSTALLATION_CHANGED")
			}
			if e = mysqlNoHistory(ctx, q, d, tenant); e != nil {
				return e
			}
			if e = recheck(); e != nil {
				return e
			}
			var count, matching int
			if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT COUNT(*),COALESCE(SUM(outcome='success' AND audience='operator' AND target_type=? AND target_id=? AND JSON_CONTAINS(metadata,CAST(? AS JSON)) AND JSON_LENGTH(metadata)=?),0) FROM audit_events WHERE tenant_id=? AND action=?`, d.target, target, string(raw), len(metadata), tenant, d.action)).Scan(&count, &matching) != nil {
				return errors.New("STORE_INITIALIZATION_AUDIT_UNAVAILABLE")
			}
			if count > 0 {
				if count != 1 || matching != 1 {
					return errors.New("STORE_INITIALIZATION_AUDIT_CONFLICT")
				}
				status = Verified
				return nil
			}
			if d.mode == check {
				status = NotRecorded
				return nil
			}
			if _, e = q.Exec(ctx, transaction.SQL(transaction.MySQL, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,'operator',?,'success',?,?,?,?,CAST(? AS JSON))`, uuid.NewString(), tenant, d.action, d.target, target, o.Reason, uuid.NewString(), string(raw))); e != nil {
				return e
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
