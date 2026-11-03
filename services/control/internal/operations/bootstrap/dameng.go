//go:build cgo && odbc_manager

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
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const damengIntentFormat = "projectone.dameng.bootstrap-intent.v1"

// DamengIntent is a secret-bearing recovery document. It is deliberately not a
// PostgreSQL Intent: its target identifies the actual Dameng installation.
type DamengIntent struct {
	Format        string  `json:"format"`
	Request       Request `json:"request"`
	RequestSHA256 string  `json:"request_sha256"`
	TargetSHA256  string  `json:"target_sha256"`
	Password      string  `json:"password"`
}

func (i DamengIntent) validate(request Request, raw []byte, binding string) error {
	secret, e := base64.RawURLEncoding.DecodeString(i.Password)
	if e != nil || len(secret) != passwordBytes || base64.RawURLEncoding.EncodeToString(secret) != i.Password || i.Format != damengIntentFormat || i.Request != request || i.RequestSHA256 != digest(raw) || i.TargetSHA256 != binding {
		return errors.New("BOOTSTRAP_INTENT_MISMATCH")
	}
	return nil
}

func damengBootstrapState(ctx context.Context, s *identity.Service) (bool, error) {
	store, e := s.Repository()
	if e != nil || store.Dialect() != transaction.Dameng {
		return false, errors.New("BOOTSTRAP_DAMENG_REPOSITORY_REQUIRED")
	}
	var complete bool
	var single int
	var tenant, workspace, admin sql.NullString
	var at sql.NullTime
	e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "BOOTSTRAP_COMPLETE","TENANT_ID","DEFAULT_WORKSPACE_ID","PRIMARY_ADMIN_USER_ID","BOOTSTRAPPED_AT",(SELECT COUNT(*) FROM "INSTALLATION_STATE") FROM "INSTALLATION_STATE" WHERE "ID"=1`)).Scan(&complete, &tenant, &workspace, &admin, &at, &single)
	if e != nil || single != 1 || complete && (!tenant.Valid || !workspace.Valid || !admin.Valid || !at.Valid) || !complete && (tenant.Valid || workspace.Valid || admin.Valid || at.Valid) {
		return false, errors.New("BOOTSTRAP_INSTALLATION_STATE_INVALID")
	}
	if !complete {
		var users, tenants, workspaces, credentials, memberships int
		if e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT (SELECT COUNT(*) FROM "USERS"),(SELECT COUNT(*) FROM "TENANTS"),(SELECT COUNT(*) FROM "WORKSPACES"),(SELECT COUNT(*) FROM "PASSWORD_CREDENTIALS"),(SELECT COUNT(*) FROM "WORKSPACE_MEMBERSHIPS") FROM DUAL`)).Scan(&users, &tenants, &workspaces, &credentials, &memberships); e != nil || users != 0 || tenants != 0 || workspaces != 0 || credentials != 0 || memberships != 0 {
			return false, errors.New("BOOTSTRAP_INITIAL_INSTALLATION_REQUIRED")
		}

	}
	return complete, nil
}

func observeDameng(ctx context.Context, s *identity.Service, i DamengIntent) (string, error) {
	complete, e := damengBootstrapState(ctx, s)
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
	e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT u."EMAIL_CANONICAL",u."DISPLAY_NAME",t."NAME",w."NAME",p."PASSWORD_PHC" FROM "INSTALLATION_STATE" i JOIN "USERS" u ON u."ID"=i."PRIMARY_ADMIN_USER_ID" JOIN "TENANTS" t ON t."ID"=i."TENANT_ID" JOIN "WORKSPACES" w ON w."ID"=i."DEFAULT_WORKSPACE_ID" AND w."TENANT_ID"=t."ID" JOIN "WORKSPACE_MEMBERSHIPS" m ON m."USER_ID"=u."ID" AND m."WORKSPACE_ID"=w."ID" AND m."TENANT_ID"=t."ID" JOIN "PASSWORD_CREDENTIALS" p ON p."USER_ID"=u."ID" JOIN "USER_PREFERENCES" pref ON pref."USER_ID"=u."ID" WHERE i."ID"=1 AND i."BOOTSTRAP_COMPLETE"=1 AND u."STATUS"='active' AND u."IS_PLATFORM_ADMIN"=1 AND t."STATUS"='active' AND w."STATUS"='active' AND m."ROLE"='owner'`)).Scan(&email, &name, &tenant, &workspace, &phc)
	if e != nil || email != i.Request.AdministratorEmail || name != i.Request.DisplayName || tenant != i.Request.InstallationName || workspace != i.Request.WorkspaceName {
		return "", errors.New("BOOTSTRAP_RESULT_CONFLICT")
	}
	ok, e := s.Passwords.Verify(ctx, phc, i.Password)
	if e != nil || !ok {
		return "", errors.New("BOOTSTRAP_CREDENTIAL_CONFLICT")
	}
	return Verified, nil
}

// RunDameng uses the exact admitted Identity owner under the native installation
// lock. It preserves an exclusive durable recovery intent before account creation.
// An uncertain Bootstrap result is returned once and requires explicit reconcile.
func RunDameng(ctx context.Context, s *identity.Service, operator *databaseaccess.DamengContext, command, input, output string) (string, error) {
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
	raw, e := readPrivate(input, &request)
	if e != nil {
		return "", e
	}
	request, e = normalize(request)
	if e != nil {
		return "", e
	}
	i := DamengIntent{Format: damengIntentFormat, Request: request, RequestSHA256: digest(raw), TargetSHA256: binding}
	var intentRaw []byte
	if command == "bootstrap-admin-file" {
		done, e := damengBootstrapState(ctx, s)
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
		var check DamengIntent
		if e = writeDocument(output, intentRaw, &check); e != nil {
			return "", e
		}
	} else {
		intentRaw, e = readPrivate(output, &i)
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
		actual, e := readPrivate(input, &current)
		if e != nil || !bytes.Equal(actual, raw) {
			return errors.New("BOOTSTRAP_REQUEST_CHANGED")
		}
		var retained DamengIntent
		actual, e = readPrivate(output, &retained)
		if e != nil || !bytes.Equal(actual, intentRaw) {
			return errors.New("BOOTSTRAP_INTENT_CHANGED")
		}
		return nil
	}
	if e = unchanged(); e != nil {
		return "", e
	}
	status, e := observeDameng(ctx, s, i)
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
	status, e = observeDameng(ctx, s, i)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RESULT_UNCERTAIN")
	}
	return status, nil
}
