package mysqloperator

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// VerifyRuntimeSelection compares literal declared targets and actual protected CA
// bytes. It never permits hostname aliases, TLS downgrade, or operator login reuse.
func (o *Context) VerifyRuntimeSelection(ctx context.Context, c mysqlstore.Configuration) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	return sameRuntimeSelection(o.settings, c)
}

func sameRuntimeSelection(op, c mysqlstore.Configuration) error {
	if op.Validate() != nil || c.Validate() != nil || c.Host != op.Host || c.Port != op.Port || c.Database != op.Database || c.ServerName != op.ServerName || c.ExpectedVersion != op.ExpectedVersion || c.User == op.User || c.PasswordFile == op.PasswordFile {
		return errors.New("MYSQL_RUNTIME_ACCESS_TARGET_MISMATCH")
	}
	a, e := ReadPrivateBytes(c.CAFile, mysqlstore.MaximumCABytes)
	if e != nil {
		return e
	}
	b, e := ReadPrivateBytes(op.CAFile, mysqlstore.MaximumCABytes)
	if e != nil || SHA256(a) != SHA256(b) {
		return errors.New("MYSQL_RUNTIME_ACCESS_CA_MISMATCH")
	}
	return nil
}

// VerifyDeclaredRuntimeSelection checks existing protected declarations before a
// connection or DDL. It does not assert login, grants, lock or schema readiness.
func VerifyDeclaredRuntimeSelection(c config.Config, runtime mysqlstore.Configuration) error {
	if c.DatabaseEngine() != config.MySQLDatabaseEngine || c.ValidateDatabaseSelection() != nil {
		return errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	raw, e := ReadPrivateBytes(c.Database.MySQLConfigurationFile, mysqlstore.MaximumConfigurationBytes)
	if e != nil {
		return e
	}
	op, e := mysqlstore.Decode(raw, nil)
	if e != nil {
		return e
	}
	return sameRuntimeSelection(op, runtime)
}

func ledgerFingerprint(ctx context.Context, c *sql.Conn, steps int) (string, error) {
	rows, e := c.QueryContext(ctx, "SELECT step,source_sha256,object_name,object_sha256,state,started_at,applied_at FROM schema_migrations ORDER BY step LIMIT ?", steps+1)
	if e != nil {
		return "", errors.New("MYSQL_RUNTIME_LEDGER_UNAVAILABLE")
	}
	defer rows.Close()
	h := sha256.New()
	encoder := json.NewEncoder(h)
	count := 0
	for rows.Next() {
		var entry struct {
			Step                              int
			Source, Object, Definition, State string
			Started                           time.Time
			Applied                           sql.NullTime
		}
		if rows.Scan(&entry.Step, &entry.Source, &entry.Object, &entry.Definition, &entry.State, &entry.Started, &entry.Applied) != nil || count >= steps || encoder.Encode(entry) != nil {
			return "", errors.New("MYSQL_RUNTIME_LEDGER_INVALID")
		}
		count++
	}
	if rows.Err() != nil || count != steps {
		return "", errors.New("MYSQL_RUNTIME_LEDGER_INVALID")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyRuntime uses the real runtime login while the caller keeps the operator
// lock. A runtime account never borrows that lock or privileged trigger metadata.
func (o *Context) VerifyRuntime(ctx context.Context, kernel *mysqlstore.Store, principal string) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	if kernel == nil || kernel.Pool() == nil || !principalPattern.MatchString(principal) {
		return errors.New("MYSQL_RUNTIME_ACCESS_LOGIN_REQUIRED")
	}
	if _, e := kernel.Check(ctx); e != nil {
		return e
	}
	c, e := kernel.Pool().Conn(ctx)
	if e != nil {
		return errors.New("MYSQL_RUNTIME_ACCESS_LOGIN_FAILED")
	}
	defer c.Close()
	t, e := readTarget(func() row { return c.QueryRowContext(ctx, targetQuery) })
	expected := o.target
	expected.Principal = principal
	if e != nil || t != expected {
		return errors.New("MYSQL_RUNTIME_ACCESS_LOGIN_TARGET_MISMATCH")
	}
	operator, e := ledgerFingerprint(ctx, o.connection, o.catalog.Steps())
	if e != nil {
		return e
	}
	runtime, e := ledgerFingerprint(ctx, c, o.catalog.Steps())
	if e != nil || runtime != operator {
		return errors.New("MYSQL_RUNTIME_ACCESS_LEDGER_MISMATCH")
	}
	return o.Verify(ctx)
}
