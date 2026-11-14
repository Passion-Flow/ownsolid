package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func (s *Service) identityStore() (transaction.Store, error) {
	if s.Storage != nil {
		if s.DB != nil {
			return nil, failure("IDENTITY_STORAGE_CONFIGURATION_CONFLICT")
		}
		return s.Storage, nil
	}
	if s.DB == nil {
		return nil, failure("IDENTITY_STORAGE_REQUIRED")
	}
	return transaction.FromPostgreSQL(s.DB)
}
func identitySQL(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}

// Bootstrap is called only by the trusted local operator entrance,which owns
// the installation lock. All durable identity state and audit share one outcome.
func (s *Service) Bootstrap(ctx context.Context, email, name, password, tenantName, workspaceName string) error {
	if s == nil || s.Passwords == nil {
		return failure("IDENTITY_BOOTSTRAP_CONFIGURATION_REQUIRED")
	}
	store, e := s.identityStore()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Config.Policy().HTTP.StartupSeconds))
	defer cancel()
	if e = ctx.Err(); e != nil {
		return e
	}
	canonical, display, e := security.Email(email)
	if e != nil {
		return e
	}
	name, e = security.Name(name)
	if e != nil {
		return e
	}
	tenantName, e = security.Name(tenantName)
	if e != nil {
		return e
	}
	workspaceName, e = security.Name(workspaceName)
	if e != nil {
		return e
	}
	password, e = s.Passwords.Policy(password, canonical, tenantName)
	if e != nil {
		return e
	}
	phc, e := s.Passwords.Hash(ctx, password)
	if e != nil {
		return e
	}
	e = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var done bool
		if e := tx.QueryRow(ctx, identitySQL(tx, "SELECT bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE", "SELECT bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE", `SELECT BOOTSTRAP_COMPLETE FROM INSTALLATION_STATE WHERE ID=1 FOR UPDATE`)).Scan(&done); e != nil {
			return e
		}
		if done {
			return failure("BOOTSTRAP_ALREADY_COMPLETE")
		}
		tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
		commands := []transaction.Statement{
			identitySQL(tx, "INSERT INTO tenants(id,name,status) VALUES($1,$2,'active')", "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", `INSERT INTO TENANTS(ID,NAME,STATUS) VALUES(?,?,'active')`, tenant, tenantName),
			identitySQL(tx, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')", `INSERT INTO WORKSPACES(ID,TENANT_ID,NAME,STATUS) VALUES(?,?,?,'active')`, workspace, tenant, workspaceName),
			identitySQL(tx, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,$4,'en','active',true)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',true)", `INSERT INTO USERS(ID,EMAIL_CANONICAL,EMAIL_DISPLAY,DISPLAY_NAME,LOCALE,STATUS,IS_PLATFORM_ADMIN) VALUES(?,?,?,?,'en','active',1)`, user, canonical, display, name),
			identitySQL(tx, "INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", `INSERT INTO PASSWORD_CREDENTIALS(USER_ID,PASSWORD_PHC) VALUES(?,?)`, user, phc),
			identityMembershipSQL(tx, user, tenant, workspace, "owner"),
			identitySQL(tx, "UPDATE installation_state SET bootstrap_complete=true,tenant_id=$1,default_workspace_id=$2,primary_admin_user_id=$3,bootstrapped_at=clock_timestamp() WHERE id=1", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", `UPDATE INSTALLATION_STATE SET BOOTSTRAP_COMPLETE=1,TENANT_ID=?,DEFAULT_WORKSPACE_ID=?,PRIMARY_ADMIN_USER_ID=?,BOOTSTRAPPED_AT=CURRENT_TIMESTAMP(6) WHERE ID=1`, tenant, workspace, user),
		}
		for _, command := range commands {
			n, e := tx.Exec(ctx, command)
			if e != nil {
				return e
			}
			if n != 1 {
				return failure("IDENTITY_WRITE_UNCONFIRMED")
			}
		}
		return audit.WriteWithin(ctx, tx, "", user, "operator", "installation.bootstrapped", "success", "installation", "", "", "")
	})
	if outcome, ok := transaction.CommitOutcome(e); ok {
		return failure(string(outcome))
	}
	var storage *transaction.Error
	if errors.As(e, &storage) && storage.Kind != transaction.CommitUnknown {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return failure("STORAGE_UNAVAILABLE")
	}
	return e
}
