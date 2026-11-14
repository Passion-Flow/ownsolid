package bootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const mysqlIntentFormat = "projectone.mysql.bootstrap-intent.v1"

// MySQLIntent is a secret-bearing recovery document. It is deliberately not a
// PostgreSQL Intent: its target identifies the actual MySQL installation.
type MySQLIntent struct {
	Format        string  `json:"format"`
	Request       Request `json:"request"`
	RequestSHA256 string  `json:"request_sha256"`
	TargetSHA256  string  `json:"target_sha256"`
	Password      string  `json:"password"`
}

func (i MySQLIntent) validate(request Request, raw []byte, binding string) error {
	secret, e := base64.RawURLEncoding.DecodeString(i.Password)
	if e != nil || len(secret) != passwordBytes || base64.RawURLEncoding.EncodeToString(secret) != i.Password || i.Format != mysqlIntentFormat || i.Request != request || i.RequestSHA256 != digest(raw) || i.TargetSHA256 != binding {
		return errors.New("BOOTSTRAP_INTENT_MISMATCH")
	}
	return nil
}

func mysqlBootstrapState(ctx context.Context, s *identity.Service) (bool, error) {
	store, e := s.Repository()
	if e != nil || store.Dialect() != transaction.MySQL {
		return false, errors.New("BOOTSTRAP_MYSQL_REPOSITORY_REQUIRED")
	}
	var complete, single bool
	var tenant, workspace, admin sql.NullString
	var at sql.NullTime
	e = store.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT bootstrap_complete,tenant_id,default_workspace_id,primary_admin_user_id,bootstrapped_at,(SELECT COUNT(*) FROM installation_state)=1 FROM installation_state WHERE id=1`)).Scan(&complete, &tenant, &workspace, &admin, &at, &single)
	if e != nil || !single || complete && (!tenant.Valid || !workspace.Valid || !admin.Valid || !at.Valid) || !complete && (tenant.Valid || workspace.Valid || admin.Valid || at.Valid) {
		return false, errors.New("BOOTSTRAP_INSTALLATION_STATE_INVALID")
	}
	if !complete {
		var empty bool
		if e = store.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT (SELECT COUNT(*) FROM users)=0 AND (SELECT COUNT(*) FROM tenants)=0 AND (SELECT COUNT(*) FROM workspaces)=0 AND (SELECT COUNT(*) FROM password_credentials)=0 AND (SELECT COUNT(*) FROM workspace_memberships)=0`)).Scan(&empty); e != nil || !empty {
			return false, errors.New("BOOTSTRAP_INITIAL_INSTALLATION_REQUIRED")
		}
	}
	return complete, nil
}

func observeMySQL(ctx context.Context, s *identity.Service, i MySQLIntent) (string, error) {
	complete, e := mysqlBootstrapState(ctx, s)
	if e != nil {
		return "", e
	}
	if !complete {
		return NotCommitted, nil
	}
	store, e := s.Repository()
	if e != nil {
		return "", e
	}
	var email, name, tenant, workspace, phc string
	e = store.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT u.email_canonical,u.display_name,t.name,w.name,p.password_phc FROM installation_state i JOIN users u ON u.id=i.primary_admin_user_id JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.id=i.default_workspace_id AND w.tenant_id=t.id JOIN workspace_memberships m ON m.user_id=u.id AND m.workspace_id=w.id AND m.tenant_id=t.id JOIN password_credentials p ON p.user_id=u.id WHERE i.id=1 AND i.bootstrap_complete=1 AND u.status='active' AND u.is_platform_admin=1 AND t.status='active' AND w.status='active' AND m.role='owner'`)).Scan(&email, &name, &tenant, &workspace, &phc)
	if e != nil || email != i.Request.AdministratorEmail || name != i.Request.DisplayName || tenant != i.Request.InstallationName || workspace != i.Request.WorkspaceName {
		return "", errors.New("BOOTSTRAP_RESULT_CONFLICT")
	}
	ok, e := s.Passwords.Verify(ctx, phc, i.Password)
	if e != nil || !ok {
		return "", errors.New("BOOTSTRAP_CREDENTIAL_CONFLICT")
	}
	return Verified, nil
}

// RunMySQL uses the selected Identity owner, never a synthetic pgx transaction.
// An uncertain Bootstrap result is returned once and requires explicit reconcile.
func RunMySQL(ctx context.Context, s *identity.Service, operator *mysqloperator.Context, command, input, output string) (string, error) {
	if command != "bootstrap-admin-file" && command != "reconcile-bootstrap-file" && command != "resume-bootstrap-file" {
		return "", errors.New("BOOTSTRAP_COMMAND_INVALID")
	}
	if s == nil || s.Passwords == nil {
		return "", errors.New("BOOTSTRAP_CONTEXT_INVALID")
	}
	if e := operator.VerifyIdentity(ctx, s); e != nil {
		return "", e
	}
	binding, e := operator.Binding(ctx)
	if e != nil {
		return "", e
	}
	var request Request
	raw, e := mysqloperator.ReadDocument(input, mysqloperator.MaximumIntentBytes, &request)
	if e != nil {
		return "", e
	}
	request, e = normalize(request)
	if e != nil {
		return "", e
	}
	i := MySQLIntent{Format: mysqlIntentFormat, Request: request, RequestSHA256: digest(raw), TargetSHA256: binding}
	var intentRaw []byte
	if command == "bootstrap-admin-file" {
		done, e := mysqlBootstrapState(ctx, s)
		if e != nil {
			return "", e
		}
		if done {
			return "", errors.New("BOOTSTRAP_ALREADY_COMPLETE")
		}
		secret := make([]byte, passwordBytes)
		if _, e = rand.Read(secret); e != nil {
			return "", errors.New("BOOTSTRAP_RANDOM_UNAVAILABLE")
		}
		i.Password = base64.RawURLEncoding.EncodeToString(secret)
		if _, e = s.Passwords.Policy(i.Password, request.AdministratorEmail, request.InstallationName); e != nil {
			return "", errors.New("BOOTSTRAP_PASSWORD_POLICY_REJECTED")
		}
		intentRaw, e = json.Marshal(i)
		if e != nil {
			return "", errors.New("BOOTSTRAP_INTENT_INVALID")
		}
		intentRaw = append(intentRaw, '\n')
		var check MySQLIntent
		if e = mysqloperator.WriteDocument(output, intentRaw, &check); e != nil {
			return "", e
		}
	} else {
		intentRaw, e = mysqloperator.ReadDocument(output, mysqloperator.MaximumIntentBytes, &i)
		if e != nil {
			return "", e
		}
		if e = i.validate(request, raw, binding); e != nil {
			return "", e
		}
	}
	unchanged := func() error {
		if e := operator.VerifyIdentity(ctx, s); e != nil {
			return e
		}
		var current Request
		actual, e := mysqloperator.ReadDocument(input, mysqloperator.MaximumIntentBytes, &current)
		if e != nil || !bytes.Equal(actual, raw) {
			return errors.New("BOOTSTRAP_REQUEST_CHANGED")
		}
		var retained MySQLIntent
		actual, e = mysqloperator.ReadDocument(output, mysqloperator.MaximumIntentBytes, &retained)
		if e != nil || !bytes.Equal(actual, intentRaw) {
			return errors.New("BOOTSTRAP_INTENT_CHANGED")
		}
		return nil
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	status, e := observeMySQL(ctx, s, i)
	if e != nil {
		return "", e
	}
	if command == "reconcile-bootstrap-file" || status == Verified {
		if e = unchanged(); e != nil {
			return "", e
		}
		return status, nil
	}
	if ctx.Err() != nil {
		return "", errors.New("BOOTSTRAP_INTERRUPTED_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	r := i.Request
	if e = s.Bootstrap(ctx, r.AdministratorEmail, r.DisplayName, i.Password, r.InstallationName, r.WorkspaceName); e != nil {
		return "", errors.New("BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED")
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	status, e = observeMySQL(ctx, s, i)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RESULT_UNCERTAIN")
	}
	return status, nil
}
