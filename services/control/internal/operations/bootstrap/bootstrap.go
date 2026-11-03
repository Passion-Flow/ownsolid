package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

const (
	formatVersion = 1
	passwordBytes = 32
	Verified      = "BOOTSTRAP_FILE_VERIFIED"
	NotCommitted  = "BOOTSTRAP_FILE_NOT_COMMITTED"
)

type Request struct {
	FormatVersion      int    `json:"format_version"`
	AdministratorEmail string `json:"administrator_email"`
	DisplayName        string `json:"display_name"`
	InstallationName   string `json:"installation_name"`
	WorkspaceName      string `json:"workspace_name"`
}

// Intent contains a secret. Never serialize it to diagnostics or command output.
type Intent struct {
	FormatVersion int     `json:"format_version"`
	Request       Request `json:"request"`
	RequestSHA256 string  `json:"request_sha256"`
	TargetSHA256  string  `json:"target_sha256"`
	Password      string  `json:"password"`
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func normalize(r Request) (Request, error) {
	if r.FormatVersion != formatVersion {
		return r, errors.New("BOOTSTRAP_DOCUMENT_VERSION_INVALID")
	}
	var e error
	r.AdministratorEmail, _, e = security.Email(r.AdministratorEmail)
	if e != nil {
		return r, errors.New("BOOTSTRAP_REQUEST_INVALID")
	}
	for _, field := range []*string{&r.DisplayName, &r.InstallationName, &r.WorkspaceName} {
		*field, e = security.Name(*field)
		if e != nil {
			return r, errors.New("BOOTSTRAP_REQUEST_INVALID")
		}
	}
	return r, nil
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func target(ctx context.Context, s *identity.Service, q queryer) (string, error) {
	var databaseName, schema, server string
	var databaseOID, schemaOID, tableOID uint32
	var port int
	e := q.QueryRow(ctx, `SELECT current_database(),current_schema(),d.oid,n.oid,'installation_state'::regclass::oid,COALESCE(inet_server_addr()::text,''),COALESCE(inet_server_port(),0) FROM pg_database d,pg_namespace n WHERE d.datname=current_database() AND n.nspname=current_schema()`).Scan(&databaseName, &schema, &databaseOID, &schemaOID, &tableOID, &server, &port)
	if e != nil {
		return "", errors.New("BOOTSTRAP_TARGET_UNAVAILABLE")
	}
	c := s.DB.Config().ConnConfig
	b, e := json.Marshal(struct {
		Configuration                        any
		Host, Database, User, PasswordDigest string
		Port                                 uint16
		DatabaseName, Schema, Server         string
		DatabaseOID, SchemaOID, TableOID     uint32
		ServerPort                           int
	}{s.Config, c.Host, c.Database, c.User, digest([]byte(c.Password)), c.Port, databaseName, schema, server, databaseOID, schemaOID, tableOID, port})
	if e != nil {
		return "", errors.New("BOOTSTRAP_TARGET_INVALID")
	}
	return digest(b), nil
}

func lockedTarget(ctx context.Context, s *identity.Service, lease *pgxpool.Conn) (string, error) {
	if s == nil || s.DB == nil || s.Passwords == nil || lease == nil {
		return "", errors.New("BOOTSTRAP_CONTEXT_INVALID")
	}
	var locked bool
	e := lease.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock' AND classid=0 AND objid=$1::oid AND objsubid=1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, database.InstallationLockID).Scan(&locked)
	if e != nil || !locked {
		return "", errors.New("BOOTSTRAP_INSTALLATION_LOCK_REQUIRED")
	}
	binding, e := target(ctx, s, lease)
	if e != nil {
		return "", e
	}
	poolBinding, e := target(ctx, s, s.DB)
	if e != nil || poolBinding != binding {
		return "", errors.New("BOOTSTRAP_TARGET_MISMATCH")
	}
	return binding, nil
}

func initialized(ctx context.Context, s *identity.Service) (bool, error) {
	var done bool
	if e := s.DB.QueryRow(ctx, "SELECT bootstrap_complete FROM installation_state WHERE id=1").Scan(&done); e != nil {
		return false, errors.New("BOOTSTRAP_TARGET_UNAVAILABLE")
	}
	return done, nil
}

func observe(ctx context.Context, s *identity.Service, intent Intent) (string, error) {
	done, e := initialized(ctx, s)
	if e != nil {
		return "", e
	}
	if !done {
		return NotCommitted, nil
	}
	var email, name, tenant, workspace, phc string
	e = s.DB.QueryRow(ctx, `SELECT u.email_canonical,u.display_name,t.name,w.name,p.password_phc FROM installation_state i JOIN users u ON u.id=i.primary_admin_user_id JOIN tenants t ON t.id=i.tenant_id JOIN workspaces w ON w.id=i.default_workspace_id AND w.tenant_id=t.id JOIN workspace_memberships m ON m.user_id=u.id AND m.workspace_id=w.id AND m.tenant_id=t.id JOIN password_credentials p ON p.user_id=u.id WHERE i.id=1 AND i.bootstrap_complete AND u.status='active' AND u.is_platform_admin AND t.status='active' AND w.status='active' AND m.role='owner'`).Scan(&email, &name, &tenant, &workspace, &phc)
	if e != nil {
		return "", errors.New("BOOTSTRAP_RESULT_CONFLICT")
	}
	r := intent.Request
	if email != r.AdministratorEmail || name != r.DisplayName || tenant != r.InstallationName || workspace != r.WorkspaceName {
		return "", errors.New("BOOTSTRAP_RESULT_CONFLICT")
	}
	ok, e := s.Passwords.Verify(ctx, phc, intent.Password)
	if e != nil || !ok {
		return "", errors.New("BOOTSTRAP_CREDENTIAL_CONFLICT")
	}
	return Verified, nil
}

// Run requires the caller's exclusive installation lease for the entire operation.
func Run(ctx context.Context, s *identity.Service, lease *pgxpool.Conn, command, input, output string) (string, error) {
	if command != "bootstrap-admin-file" && command != "reconcile-bootstrap-file" && command != "resume-bootstrap-file" {
		return "", errors.New("BOOTSTRAP_COMMAND_INVALID")
	}
	binding, e := lockedTarget(ctx, s, lease)
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
	intent := Intent{FormatVersion: formatVersion, Request: request, RequestSHA256: digest(raw), TargetSHA256: binding}
	if command == "bootstrap-admin-file" {
		done, e := initialized(ctx, s)
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
		intent.Password = base64.RawURLEncoding.EncodeToString(secret)
		if _, e = s.Passwords.Policy(intent.Password, request.AdministratorEmail, request.InstallationName); e != nil {
			return "", errors.New("BOOTSTRAP_PASSWORD_POLICY_REJECTED")
		}
		b, e := json.Marshal(intent)
		if e != nil {
			return "", errors.New("BOOTSTRAP_INTENT_INVALID")
		}
		if e = writeIntent(output, append(b, '\n')); e != nil {
			return "", e
		}
	} else {
		var retained Intent
		if _, e = readPrivate(output, &retained); e != nil {
			return "", e
		}
		secret, e := base64.RawURLEncoding.DecodeString(retained.Password)
		if e != nil || len(secret) != passwordBytes || base64.RawURLEncoding.EncodeToString(secret) != retained.Password || retained.FormatVersion != formatVersion || retained.Request != request || retained.RequestSHA256 != intent.RequestSHA256 || retained.TargetSHA256 != binding {
			return "", errors.New("BOOTSTRAP_INTENT_MISMATCH")
		}
		intent = retained
		status, e := observe(ctx, s, intent)
		if e != nil || command == "reconcile-bootstrap-file" || status == Verified {
			return status, e
		}
	}
	if e = ctx.Err(); e != nil {
		return "", errors.New("BOOTSTRAP_INTERRUPTED_RECONCILE_REQUIRED")
	}
	r := intent.Request
	if e = s.Bootstrap(ctx, r.AdministratorEmail, r.DisplayName, intent.Password, r.InstallationName, r.WorkspaceName); e != nil {
		return "", errors.New("BOOTSTRAP_MUTATION_UNCERTAIN_RECONCILE_REQUIRED")
	}
	status, e := observe(ctx, s, intent)
	if e != nil {
		return "", e
	}
	if status != Verified {
		return "", errors.New("BOOTSTRAP_RESULT_UNCERTAIN")
	}
	return status, nil
}
