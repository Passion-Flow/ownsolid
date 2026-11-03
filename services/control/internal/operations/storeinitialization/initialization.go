// Package storeinitialization owns fresh namespace initialization and explicit
// recovery of its non-atomic filesystem/audit boundary under an installation lease.
package storeinitialization

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const Verified = "STORE_INITIALIZATION_VERIFIED"
const NotRecorded = "STORE_INITIALIZATION_NOT_RECORDED"
const initialize = "initialize"
const check = "check"
const record = "record"

type Options struct {
	Command, Root, Reason string
	UID, GID              int
	ConfirmEmpty          bool
}
type definition struct{ kind, mode, root, action, target, history string }

func describe(c config.Config, command string) (definition, error) {
	d := definition{}
	for _, kind := range []string{"dataset", "training", "preparation"} {
		switch command {
		case "initialize-" + kind + "-store":
			d.kind, d.mode = kind, initialize
		case "check-" + kind + "-store-initialization":
			d.kind, d.mode = kind, check
		case "record-" + kind + "-store-initialization":
			d.kind, d.mode = kind, record
		}
	}
	switch d.kind {
	case "dataset":
		d.root = c.Datasets.ArtifactRoot
		d.action = "dataset.store_initialize_verified"
		d.target = "dataset_store"
		d.history = "SELECT EXISTS(SELECT 1 FROM datasets WHERE tenant_id=$1)"
	case "training":
		d.root = c.Training.ArtifactRoot
		d.action = "training.store_initialize_verified"
		d.target = "training_store"
		d.history = "SELECT EXISTS(SELECT 1 FROM training_jobs WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM training_uploads WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM training_checkpoints WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM training_artifacts WHERE tenant_id=$1)"
	case "preparation":
		d.root = c.ModelPreparation.ArtifactRoot
		d.action = "model.preparation_store_initialize_verified"
		d.target = "model_preparation_store"
		d.history = "SELECT EXISTS(SELECT 1 FROM model_preparations WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM model_preparation_uploads WHERE tenant_id=$1) OR EXISTS(SELECT 1 FROM model_preparation_artifacts WHERE tenant_id=$1)"
	default:
		return d, errors.New("STORE_INITIALIZATION_COMMAND_INVALID")
	}
	if c.AccountRuntime() && (!(c.DatasetRuntime() && d.kind == "dataset") && !(c.TrainingRuntime() && d.kind == "training") && !(c.LifecycleRuntime() && d.kind == "preparation")) {
		return d, errors.New("STORE_INITIALIZATION_PROFILE_DISABLED")
	}
	return d, nil
}
func Supported(command string) bool { _, e := describe(config.Config{}, command); return e == nil }
func requireLease(ctx context.Context, db *pgxpool.Pool, lease *pgxpool.Conn) error {
	if db == nil || lease == nil {
		return errors.New("STORE_INITIALIZATION_CONTEXT_INVALID")
	}
	poolTarget, leaseTarget := db.Config().ConnConfig, lease.Conn().Config()
	if poolTarget.Host != leaseTarget.Host || poolTarget.Port != leaseTarget.Port || poolTarget.Database != leaseTarget.Database || poolTarget.User != leaseTarget.User || poolTarget.Password != leaseTarget.Password {
		return errors.New("STORE_INITIALIZATION_TARGET_MISMATCH")
	}
	var locked bool
	if e := lease.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock' AND classid=0 AND objid=$1::oid AND objsubid=1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, database.InstallationLockID).Scan(&locked); e != nil || !locked {
		return errors.New("STORE_INITIALIZATION_LOCK_REQUIRED")
	}
	const target = `SELECT current_database()||':'||current_schema()||':'||current_user||':'||'installation_state'::regclass::oid::text||':'||(SELECT oid::text FROM pg_database WHERE datname=current_database())`
	var a, b string
	if e := lease.QueryRow(ctx, target).Scan(&a); e != nil {
		return errors.New("STORE_INITIALIZATION_TARGET_UNAVAILABLE")
	}
	if e := db.QueryRow(ctx, target).Scan(&b); e != nil || a != b {
		return errors.New("STORE_INITIALIZATION_TARGET_MISMATCH")
	}
	return nil
}
func Run(ctx context.Context, db *pgxpool.Pool, lease *pgxpool.Conn, c config.Config, o Options) (string, error) {
	d, e := describe(c, o.Command)
	if e != nil {
		return "", e
	}
	if e = requireLease(ctx, db, lease); e != nil {
		return "", e
	}
	if e = database.Check(ctx, db, c.Server.Profile()); e != nil {
		return "", e
	}
	if d.root == "" || o.Root != d.root {
		return "", errors.New("STORE_INITIALIZATION_CONFIGURED_ROOT_REQUIRED")
	}
	if d.mode == initialize && !o.ConfirmEmpty {
		return "", errors.New("STORE_INITIALIZATION_EMPTY_CONFIRMATION_REQUIRED")
	}
	if d.mode != check && !modelpreparations.ValidOperatorReason(o.Reason) {
		return "", errors.New("OPERATOR_REASON_REQUIRED")
	}
	var tenant string
	if e = db.QueryRow(ctx, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete").Scan(&tenant); e != nil {
		return "", errors.New("STORE_INITIALIZATION_INSTALLATION_REQUIRED")
	}
	var retained bool
	if e = db.QueryRow(ctx, d.history, tenant).Scan(&retained); e != nil {
		return "", errors.New("STORE_INITIALIZATION_HISTORY_UNAVAILABLE")
	}
	if retained {
		return "", errors.New("STORE_INITIALIZATION_RESTORE_REQUIRED")
	}
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
	var store string
	switch d.kind {
	case "dataset":
		store, e = datasets.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	case "training":
		store, e = training.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	case "preparation":
		store, e = modelpreparations.VerifyInitializedStore(o.Root, tenant, o.UID, o.GID)
	}
	if e != nil {
		return "", e
	}
	var count, matching int
	if e = db.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome='success' AND target_type=$4 AND metadata->>'store_id'=$3::text AND metadata->>'runtime_uid'=$5 AND metadata->>'runtime_gid'=$6 AND metadata->>'source'='protected_local_operator' AND metadata->>'referenced_files_verified'='true') FROM audit_events WHERE tenant_id=$1 AND action=$2 AND target_id=$3::uuid`, tenant, d.action, store, d.target, strconv.Itoa(o.UID), strconv.Itoa(o.GID)).Scan(&count, &matching); e != nil {
		return "", errors.New("STORE_INITIALIZATION_AUDIT_UNAVAILABLE")
	}
	if count > 0 {
		if count != 1 || matching != 1 {
			return "", errors.New("STORE_INITIALIZATION_AUDIT_CONFLICT")
		}
		return Verified, nil
	}
	if d.mode == check {
		return NotRecorded, nil
	}
	metadata, _ := json.Marshal(map[string]any{"store_id": store, "runtime_uid": o.UID, "runtime_gid": o.GID, "source": "protected_local_operator", "referenced_files_verified": true})
	if _, e = db.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(gen_random_uuid(),clock_timestamp(),$1,'operator',$2,'success',$3,$4,$5,gen_random_uuid(),$6)`, tenant, d.action, d.target, store, o.Reason, metadata); e != nil {
		return "", errors.New("STORE_INITIALIZATION_AUDIT_WRITE_UNCERTAIN")
	}
	return Verified, nil
}
