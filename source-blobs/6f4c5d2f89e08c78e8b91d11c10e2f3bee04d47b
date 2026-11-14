package databaseaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const mysqlRuntimeIntentFormat = "projectone.mysql.runtime-intent.v1"
const mysqlRestoredIntentFormat = "projectone.mysql.restored-runtime-intent.v1"
const mysqlCreatedFormat = "projectone.mysql.runtime-created.v1"
const MySQLLockedPartial = "MYSQL_RUNTIME_ACCESS_OWNED_LOCKED_PARTIAL"
const MySQLAuditMissing = "MYSQL_RUNTIME_ACCESS_AUDIT_NOT_RECORDED"
const ReconcileMySQLCommand = "reconcile-runtime-access"
const ResumeMySQLCommand = "resume-runtime-access"

var mysqlDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type mysqlRuntimeIntent struct {
	Format        string `json:"format"`
	ID            string `json:"intent_id"`
	TargetSHA256  string `json:"target_sha256"`
	InputSHA256   string `json:"input_sha256"`
	RestoreSHA256 string `json:"restore_context_sha256,omitempty"`
}
type mysqlCreatedReceipt struct {
	Format              string `json:"format"`
	IntentSHA256        string `json:"intent_sha256"`
	AuthenticatorSHA256 string `json:"authenticator_sha256"`
}

func (i mysqlRuntimeIntent) validate(binding string, input MySQLRuntimeInput) error {
	id, e := uuid.Parse(i.ID)
	if e != nil || id.String() != i.ID || id.Version() != 4 || !validMySQLIntentVariant(i.Format, i.RestoreSHA256) || !mysqlDigest.MatchString(i.TargetSHA256) || !mysqlDigest.MatchString(i.InputSHA256) || i.TargetSHA256 != binding || i.InputSHA256 != input.fingerprint {
		return errors.New("MYSQL_RUNTIME_ACCESS_INTENT_MISMATCH")
	}
	return nil
}
func validMySQLIntentVariant(format, restoreSHA string) bool {
	return format == mysqlRuntimeIntentFormat && restoreSHA == "" || format == mysqlRestoredIntentFormat && mysqlDigest.MatchString(restoreSHA)
}
func mysqlRuntimeAuditAction(i mysqlRuntimeIntent) string {
	if i.Format == mysqlRestoredIntentFormat {
		return "operator.runtime_access_restored"
	}
	return "operator.runtime_access_prepared"
}
func mysqlMarshal(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, errors.New("MYSQL_RUNTIME_ACCESS_DOCUMENT_INVALID")
	}
	return append(raw, '\n'), nil
}
func mysqlCreateStatement(i MySQLRuntimeInput, intent mysqlRuntimeIntent, sha string) (string, error) {
	account, e := mysqlAccount(i)
	if e != nil || !credentialValid(i.password) {
		return "", errors.New("RUNTIME_CREDENTIAL_FORMAT_INVALID")
	}
	if intent.validate(intent.TargetSHA256, i) != nil || !mysqlDigest.MatchString(sha) {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_INTENT_INVALID")
	}
	attribute, e := json.Marshal(mysqlAccountAttribute{intent.ID, sha})
	if e != nil {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_INTENT_INVALID")
	}
	// Each interpolated value has a named strict alphabet, including the existing
	// credential contract. MySQL's password grammar does not allow a '?' marker.
	return "CREATE USER " + account + " IDENTIFIED WITH caching_sha2_password BY '" + i.password + "' REQUIRE SSL WITH MAX_USER_CONNECTIONS " + strconv.Itoa(i.connection.Policy.MaximumOpenConnections) + " PASSWORD EXPIRE NEVER ACCOUNT LOCK ATTRIBUTE '" + string(attribute) + "'", nil
}

func PrepareMySQL(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput) (string, error) {
	return mysqlAccess(ctx, o, i, "prepare")
}
func CheckMySQL(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput) (string, error) {
	return mysqlAccess(ctx, o, i, "check")
}
func ReconcileMySQL(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput) (string, error) {
	return mysqlAccess(ctx, o, i, "reconcile")
}
func ResumeMySQL(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput) (string, error) {
	return mysqlAccess(ctx, o, i, "resume")
}

func mysqlInitialInstallation(ctx context.Context, q transaction.Executor) error {
	var empty bool
	if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT (SELECT COUNT(*) FROM installation_state)=1 AND NOT bootstrap_complete AND tenant_id IS NULL AND default_workspace_id IS NULL AND primary_admin_user_id IS NULL AND bootstrapped_at IS NULL AND (SELECT COUNT(*) FROM users)=0 AND (SELECT COUNT(*) FROM tenants)=0 AND (SELECT COUNT(*) FROM workspaces)=0 AND (SELECT COUNT(*) FROM password_credentials)=0 AND (SELECT COUNT(*) FROM workspace_memberships)=0 FROM installation_state WHERE id=1`)).Scan(&empty) != nil || !empty {
		return errors.New("MYSQL_RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED")
	}
	return nil
}

func mysqlAccess(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput, mode string) (string, error) {
	return mysqlAccessWithProof(ctx, o, i, mode, nil)
}
func mysqlAccessWithProof(ctx context.Context, o *mysqloperator.Context, i MySQLRuntimeInput, mode string, proof *mysqlRestoreProof) (string, error) {
	if mode != "prepare" && mode != "check" && mode != "reconcile" && mode != "resume" {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_COMMAND_INVALID")
	}
	if e := i.verify(); e != nil {
		return "", e
	}
	if e := o.VerifyRuntimeSelection(ctx, i.connection); e != nil {
		return "", e
	}
	if e := o.VerifyInstallation(ctx); e != nil {
		return "", e
	}
	lease, e := o.Lease(ctx)
	if e != nil {
		return "", e
	}
	if e = mysqlRequirePrivilegeVisibility(ctx, lease); e != nil {
		return "", e
	}
	if e = mysqlCheckTables(ctx, lease, i); e != nil {
		return "", e
	}
	store, e := o.Store(ctx)
	if e != nil {
		return "", e
	}
	binding, e := o.Binding(ctx)
	if e != nil {
		return "", e
	}
	path := i.configuration.Database.MySQLRuntimeAccess.IntentFile
	intent := mysqlRuntimeIntent{Format: mysqlRuntimeIntentFormat, ID: uuid.NewString(), TargetSHA256: binding, InputSHA256: i.fingerprint}
	if proof != nil {
		intent.Format = mysqlRestoredIntentFormat
		intent.RestoreSHA256 = proof.sha
	}
	mutationAllowed := func() error {
		if proof == nil {
			return mysqlInitialInstallation(ctx, store)
		}
		return proof.verify(true)
	}
	restoredMutationAllowed := func() error {
		if proof == nil {
			return nil
		}
		return proof.verify(true)
	}
	var raw []byte
	if mode == "prepare" {
		if e = mutationAllowed(); e != nil {
			return "", e
		}
		count, e := mysqlUserCount(ctx, lease, i.connection.User)
		if e != nil {
			return "", e
		}
		if count != 0 {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_EXISTING_ACCOUNT_NOT_ADOPTED")
		}
		if _, e = os.Lstat(path + ".created"); !os.IsNotExist(e) {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_CREATED_RECEIPT_CONFLICT")
		}
		raw, e = mysqlMarshal(intent)
		if e != nil {
			return "", e
		}
		var check mysqlRuntimeIntent
		if e = mysqloperator.WriteDocument(path, raw, &check); e != nil {
			return "", e
		}
	} else {
		raw, e = mysqloperator.ReadDocument(path, mysqloperator.MaximumIntentBytes, &intent)
		if e != nil {
			return "", e
		}
		if e = intent.validate(binding, i); e != nil {
			return "", e
		}
		if proof == nil && intent.Format != mysqlRuntimeIntentFormat || proof != nil && (intent.Format != mysqlRestoredIntentFormat || intent.RestoreSHA256 != proof.sha) {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_RESTORE_INTENT_MISMATCH")
		}
	}
	intentSHA := mysqloperator.SHA256(raw)
	unchanged := func() error {
		if proof != nil {
			if e := proof.verify(false); e != nil {
				return e
			}
		}
		if e := i.verify(); e != nil {
			return e
		}
		if e := o.VerifyRuntimeSelection(ctx, i.connection); e != nil {
			return e
		}
		var retained mysqlRuntimeIntent
		current, e := mysqloperator.ReadDocument(path, mysqloperator.MaximumIntentBytes, &retained)
		if e != nil || !bytes.Equal(current, raw) {
			return errors.New("MYSQL_RUNTIME_ACCESS_INTENT_CHANGED")
		}
		return nil
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	state, e := mysqlInspect(ctx, lease, i, intent.ID, intentSHA)
	if e != nil {
		return "", e
	}
	if !state.exists {
		if _, e = os.Lstat(path + ".created"); !os.IsNotExist(e) {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_CREATED_ACCOUNT_MISSING")
		}
		if mode == "check" || mode == "reconcile" {
			if e = unchanged(); e != nil {
				return "", e
			}
			return NotCommitted, nil
		}
		if e = mutationAllowed(); e != nil {
			return "", e
		}
		if _, e = os.Lstat(path + ".created"); !os.IsNotExist(e) {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_CREATED_RECEIPT_CONFLICT")
		}
		statement, e := mysqlCreateStatement(i, intent, intentSHA)
		if e != nil {
			return "", e
		}
		if e = unchanged(); e != nil {
			return "", e
		}
		if _, e = lease.ExecContext(ctx, statement); e != nil {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_CREATE_UNCERTAIN_REVIEW_REQUIRED")
		}
		if e = unchanged(); e != nil {
			return "", e
		}
		state, e = mysqlInspect(ctx, lease, i, intent.ID, intentSHA)
		if e != nil {
			return "", e
		}
		if !state.exists || !state.locked || len(state.grants) != 0 {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_CREATED_ACCOUNT_CONFLICT")
		}
		receipt := mysqlCreatedReceipt{mysqlCreatedFormat, intentSHA, state.authenticator}
		b, e := mysqlMarshal(receipt)
		if e != nil {
			return "", e
		}
		var check mysqlCreatedReceipt
		if e = mysqloperator.WriteDocument(path+".created", b, &check); e != nil {
			return "", e
		}
	}
	var receipt mysqlCreatedReceipt
	receiptRaw, e := mysqloperator.ReadDocument(path+".created", mysqloperator.MaximumIntentBytes, &receipt)
	if e != nil || receipt.Format != mysqlCreatedFormat || receipt.IntentSHA256 != intentSHA || receipt.AuthenticatorSHA256 != state.authenticator {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_AUTHENTICATOR_RECEIPT_REQUIRED")
	}
	checkReceipt := func() error {
		var retained mysqlCreatedReceipt
		current, e := mysqloperator.ReadDocument(path+".created", mysqloperator.MaximumIntentBytes, &retained)
		if e != nil || !bytes.Equal(current, receiptRaw) {
			return errors.New("MYSQL_RUNTIME_ACCESS_AUTHENTICATOR_RECEIPT_CHANGED")
		}
		return unchanged()
	}
	if e = checkReceipt(); e != nil {
		return "", e
	}
	expected := mysqlExpectedGrants()
	if state.locked && (mode == "check" || mode == "reconcile") {
		return MySQLLockedPartial, nil
	}
	if !state.locked && len(state.grants) != len(expected) {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_UNLOCKED_PARTIAL_CONFLICT")
	}
	if state.locked {
		if e = restoredMutationAllowed(); e != nil {
			return "", e
		}
		account, e := mysqlAccount(i)
		if e != nil {
			return "", e
		}
		for _, table := range append(mysqlTables(), "schema_migrations") {
			var missing []string
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
				key := table + ":" + privilege
				if expected[key] && !state.grants[key] {
					missing = append(missing, privilege)
				}
			}
			if len(missing) == 0 {
				continue
			}
			if e = checkReceipt(); e != nil {
				return "", e
			}
			if _, e = lease.ExecContext(ctx, "GRANT "+strings.Join(missing, ",")+" ON `"+i.connection.Database+"`.`"+table+"` TO "+account); e != nil {
				return "", errors.New("MYSQL_RUNTIME_ACCESS_GRANT_UNCERTAIN_RECONCILE_REQUIRED")
			}
		}
		if e = checkReceipt(); e != nil {
			return "", e
		}
		state, e = mysqlInspect(ctx, lease, i, intent.ID, intentSHA)
		if e != nil {
			return "", e
		}
		if !state.exists || !state.locked || state.authenticator != receipt.AuthenticatorSHA256 || len(state.grants) != len(expected) {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_PREUNLOCK_CONFLICT")
		}
		if e = restoredMutationAllowed(); e != nil {
			return "", e
		}
		if _, e = lease.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); e != nil {
			return "", errors.New("MYSQL_RUNTIME_ACCESS_UNLOCK_UNCERTAIN_RECONCILE_REQUIRED")
		}
	}
	if e = checkReceipt(); e != nil {
		return "", e
	}
	state, e = mysqlInspect(ctx, lease, i, intent.ID, intentSHA)
	if e != nil {
		return "", e
	}
	if !state.exists || state.locked || state.authenticator != receipt.AuthenticatorSHA256 || len(state.grants) != len(expected) {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_RESULT_CONFLICT")
	}
	kernel, _, e := mysqlstore.Open(ctx, i.connection)
	if e != nil {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_LOGIN_FAILED")
	}
	if !kernel.InputMatches(i.connection, mysqloperator.SHA256([]byte(i.password)), i.caSHA256) {
		if kernel.Close() != nil {
			return "", errors.New("MYSQL_RUNTIME_CONNECTOR_INPUT_MISMATCH_CLOSE_FAILED")
		}
		return "", errors.New("MYSQL_RUNTIME_ACCESS_CONNECTOR_INPUT_MISMATCH")
	}
	loginError := o.VerifyRuntime(ctx, kernel, i.connection.User+"@"+i.configuration.Database.MySQLRuntimeAccess.AccountHost)
	closeError := kernel.Close()
	if loginError != nil {
		return "", loginError
	}
	if closeError != nil {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_LOGIN_CLOSE_FAILED")
	}
	if e = checkReceipt(); e != nil {
		return "", e
	}
	recorded, e := mysqlAuditRecorded(ctx, store, intent, intentSHA)
	if e != nil {
		return "", e
	}
	if !recorded {
		if mode == "check" || mode == "reconcile" {
			return MySQLAuditMissing, nil
		}
		if e = restoredMutationAllowed(); e != nil {
			return "", e
		}
		if e = mysqlRecordAudit(ctx, o, store, i.connection.Database, intent, intentSHA); e != nil {
			return "", e
		}
	}
	if e = checkReceipt(); e != nil {
		return "", e
	}
	recorded, e = mysqlAuditRecorded(ctx, store, intent, intentSHA)
	if e != nil {
		return "", e
	}
	if !recorded {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_AUDIT_UNCERTAIN")
	}
	state, e = mysqlInspect(ctx, lease, i, intent.ID, intentSHA)
	if e != nil {
		return "", e
	}
	if !state.exists || state.locked || state.authenticator != receipt.AuthenticatorSHA256 || len(state.grants) != len(expected) {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_FINAL_ACCOUNT_CONFLICT")
	}
	if e = checkReceipt(); e != nil {
		return "", e
	}
	return Verified, nil
}

func mysqlAuditRecorded(ctx context.Context, q transaction.Executor, i mysqlRuntimeIntent, sha string) (bool, error) {
	var count, matching int
	e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT COUNT(*),COALESCE(SUM(action=? AND outcome='success' AND audience='operator' AND actor_user_id IS NULL AND subject_user_id IS NULL AND target_type='runtime_access' AND target_id=? AND reason=?),0) FROM audit_events WHERE request_id=?`, mysqlRuntimeAuditAction(i), i.ID, "intent_sha256:"+sha, i.ID)).Scan(&count, &matching)
	if e != nil || count > 1 || matching != count {
		return false, errors.New("MYSQL_RUNTIME_ACCESS_AUDIT_CONFLICT")
	}
	return count == 1, nil
}
func mysqlRecordAudit(ctx context.Context, o *mysqloperator.Context, store transaction.Store, database string, i mysqlRuntimeIntent, sha string) error {
	lease, e := o.Lease(ctx)
	if e != nil {
		return e
	}
	var connection uint64
	if lease.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connection) != nil || connection == 0 {
		return errors.New("MYSQL_RUNTIME_ACCESS_LOCK_UNAVAILABLE")
	}
	lock, e := mysqlmigrate.LockName(database)
	if e != nil {
		return e
	}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var held bool
		if q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT COALESCE(IS_USED_LOCK(?)=?,FALSE)", lock, connection)).Scan(&held) != nil || !held {
			return errors.New("MYSQL_RUNTIME_ACCESS_LOCK_UNAVAILABLE")
		}
		var singleton int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.MySQL, "SELECT id FROM installation_state WHERE id=1 FOR UPDATE")).Scan(&singleton); e != nil || singleton != 1 {
			return errors.New("MYSQL_RUNTIME_ACCESS_INSTALLATION_UNAVAILABLE")
		}
		present, e := mysqlAuditRecorded(ctx, q, i, sha)
		if e != nil || present {
			return e
		}
		return audit.WriteWithin(ctx, q, "", "", "operator", mysqlRuntimeAuditAction(i), "success", "runtime_access", i.ID, "intent_sha256:"+sha, i.ID)
	})
	if e != nil {
		return errors.New("MYSQL_RUNTIME_ACCESS_AUDIT_UNCERTAIN_RECONCILE_REQUIRED")
	}
	return nil
}
