//go:build cgo && odbc_manager

package damengserving

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const exclusiveInstallationLockMode = 6

// Full control repositories include projections wider than the schema-only
// ten-column minimum. Reserve the declared 32-column serving contract.
const minimumControlProjectionColumns = 32

type runtimeOwner struct {
	mu                                  sync.Mutex
	kernel                              *dameng.Store
	store                               transaction.Store
	lease                               *sql.Conn
	schema                              dameng.SchemaConfiguration
	lockID                              int64
	acquired, poisoned, closing, closed bool
	binding                             installationBinding
}
type installationBinding struct {
	Schema, Principal, Deployment string
	ObjectID                      int64
}

func (*runtimeOwner) String() string                  { return "DAMENG_SERVING_OWNER_REDACTED" }
func (*runtimeOwner) GoString() string                { return "DAMENG_SERVING_OWNER_REDACTED" }
func (r *runtimeOwner) Repository() transaction.Store { return r.store }
func (r *runtimeOwner) CleanupSeconds() int           { return r.schema.CleanupSeconds }

// OpenProtected uses the existing private-file parsers and native input binding.
// There is no owner connection, plaintext DSN, implicit grant, fallback or reopen.
func OpenProtected(ctx context.Context, runtimeFile, schemaFile string, minimumConnections int) (Runtime, error) {
	if ctx == nil || minimumConnections < 2 {
		return nil, errors.New("DAMENG_SERVING_INPUT_INVALID")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("DAMENG_SERVING_STARTUP_DEADLINE_REQUIRED")
	}
	c, e := dameng.LoadConnectionConfiguration(runtimeFile, "")
	if e != nil {
		return nil, e
	}
	schema, e := dameng.LoadSchemaConfiguration(schemaFile, "")
	if e != nil {
		return nil, e
	}
	if c.Schema != schema.Schema || c.User != schema.RuntimeUser || c.User == c.Schema || c.Policy.MaximumOpenConnections < minimumConnections || c.Policy.MaximumPhysicalConnections < minimumConnections || c.CleanupSeconds > schema.CleanupSeconds || c.Policy.MaximumColumns < minimumControlProjectionColumns {
		return nil, errors.New("DAMENG_SERVING_RUNTIME_CONFIGURATION_MISMATCH")
	}
	// Central schema owner supplies the complete business catalog gate. It must
	// exist before native compilation; do not replace it with a step-count guess.
	if e = migrate.RequireControlServingCatalog(); e != nil {
		return nil, e
	}
	r := &runtimeOwner{schema: schema, lockID: migrate.InstallationLockID(schema.Schema)}
	r.kernel, _, e = dameng.Open(ctx, c)
	if e != nil {
		if r.kernel == nil {
			return nil, e
		}
		return r, e
	}
	r.store, e = transaction.FromDameng(r.kernel)
	if e != nil {
		return r, e
	}
	pool, e := r.kernel.BorrowPool()
	if e != nil {
		return r, e
	}
	r.lease, e = pool.Conn(ctx)
	if e != nil {
		return r, errors.New("DAMENG_INSTALLATION_SESSION_UNAVAILABLE")
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(schema.LockWaitSeconds+schema.StepSeconds)*time.Second)
	var code int
	e = r.lease.QueryRowContext(operation, "SELECT DBMS_LOCK.REQUEST(?,?,?,FALSE) FROM DUAL", r.lockID, exclusiveInstallationLockMode, schema.LockWaitSeconds).Scan(&code)
	cancel()
	if e != nil {
		r.poisoned = true
		return r, errors.New("INSTALLATION_LOCK_OUTCOME_UNKNOWN")
	}
	if code != 0 {
		return r, errors.New("INSTALLATION_PROCESS_ALREADY_RUNNING")
	}
	r.acquired = true
	if e = r.verifyLocked(ctx, true); e != nil {
		return r, e
	}
	return r, nil
}

func (r *runtimeOwner) verifyLock(ctx context.Context) error {
	if r.lease == nil || !r.acquired || r.poisoned {
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	operation, cancel := context.WithTimeout(ctx, time.Duration(r.schema.StepSeconds)*time.Second)
	defer cancel()
	var code int
	// CONVERT2 verifies that this same session still owns the exclusive lock. It
	// never requests a new lock after a loss. Zero wait and mode are fixed semantics.
	if e := r.lease.QueryRowContext(operation, "SELECT DBMS_LOCK.CONVERT2(?,?,0) FROM DUAL", r.lockID, exclusiveInstallationLockMode).Scan(&code); e != nil {
		r.poisoned = true
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	if code != 0 {
		r.poisoned = true
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	return nil
}
func (r *runtimeOwner) verifyLocked(ctx context.Context, initial bool) error {
	if r.kernel == nil || r.store == nil || r.closing || r.closed {
		return errors.New("DAMENG_SERVING_OWNER_UNAVAILABLE")
	}
	if e := r.verifyLock(ctx); e != nil {
		return e
	}
	report, e := migrate.CheckRuntime(ctx, r.kernel, r.schema)
	if e != nil {
		return e
	}
	if !report.RuntimeReady || !report.FoundationReady {
		return errors.New("DAMENG_CONTROL_SCHEMA_NOT_READY")
	}
	var binding installationBinding
	e = r.store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA'),SYS_CONTEXT('USERENV','SESSION_USER'),l."DEPLOYMENT_ID",a.OBJECT_ID FROM "PLATFORM_LICENSE_STATE" l JOIN ALL_OBJECTS a ON a.OWNER=? AND a.OBJECT_NAME='INSTALLATION_STATE' AND a.OBJECT_TYPE='TABLE' WHERE l."ID"=1`, r.schema.Schema)).Scan(&binding.Schema, &binding.Principal, &binding.Deployment, &binding.ObjectID)
	id, parse := uuid.Parse(binding.Deployment)
	if e != nil || parse != nil || id == uuid.Nil || id.String() != binding.Deployment || binding.Schema != r.schema.Schema || binding.Principal != r.schema.RuntimeUser || binding.ObjectID <= 0 {
		return errors.New("DAMENG_SERVING_INSTALLATION_CHANGED")
	}
	if initial {
		r.binding = binding
	} else if binding != r.binding {
		return errors.New("DAMENG_SERVING_INSTALLATION_CHANGED")
	}
	return r.verifyLock(ctx)
}
func (r *runtimeOwner) Verify(ctx context.Context) error {
	if ctx == nil {
		return errors.New("DAMENG_SERVING_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("DAMENG_SERVING_DEADLINE_REQUIRED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.verifyLocked(ctx, false)
}
func (r *runtimeOwner) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("DAMENG_SERVING_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("DAMENG_SERVING_DEADLINE_REQUIRED")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closing = true
	var result error
	if r.lease != nil {
		if r.acquired && !r.poisoned {
			var code int
			operation, cancel := context.WithTimeout(ctx, time.Duration(r.schema.CleanupSeconds)*time.Second)
			e := r.lease.QueryRowContext(operation, "SELECT DBMS_LOCK.RELEASE(?) FROM DUAL", r.lockID).Scan(&code)
			cancel()
			if e != nil || code != 0 {
				result = errors.New("INSTALLATION_LOCK_RELEASE_UNCONFIRMED")
				r.poisoned = true
			}
		}
		if e := r.lease.Close(); e != nil {
			result = errors.Join(result, errors.New("DAMENG_INSTALLATION_SESSION_CLOSE_UNCONFIRMED"))
		}
		r.lease = nil
		r.acquired = false
	}
	if r.kernel != nil {
		_, e := r.kernel.Close(ctx)
		if e != nil {
			return errors.Join(result, dameng.SafeError(e))
		}
		r.kernel = nil
	}
	r.closed = true
	return result
}
