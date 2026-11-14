package mysqloperator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// FreshTarget intentionally has no installation claim before native SQL import.
type FreshTarget struct {
	ServerUUID string `json:"server_uuid"`
	Database   string `json:"database"`
	Principal  string `json:"principal"`
	Version    string `json:"version"`
}

// Fresh borrows the same actual protected connector and held installation lock.
// Its existence proves no schema, journal, bootstrap, restore or runtime readiness.
type Fresh struct {
	kernel        *mysqlstore.Store
	connection    *sql.Conn
	configuration config.Config
	catalog       mysqlmigrate.Catalog
	options       mysqlmigrate.Options
	settings      mysqlstore.Configuration
	target        FreshTarget
	connectionID  uint64
	inputs        string
}

func freshTarget(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (FreshTarget, uint64, error) {
	var t FreshTarget
	var id uint64
	if q.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE(),CURRENT_USER(),VERSION(),CONNECTION_ID()").Scan(&t.ServerUUID, &t.Database, &t.Principal, &t.Version, &id) != nil || !uuidPattern.MatchString(t.ServerUUID) || t.ServerUUID == "00000000-0000-0000-0000-000000000000" || !principalPattern.MatchString(t.Principal) || t.Database == "" || t.Version == "" || id == 0 {
		return FreshTarget{}, 0, errors.New("MYSQL_OPERATOR_TARGET_UNAVAILABLE")
	}
	return t, id, nil
}
func freshInputs(kernel *mysqlstore.Store, c config.Config) (mysqlstore.Configuration, string, error) {
	settings, files, e := configurationInputs(kernel, c)
	if e != nil {
		return mysqlstore.Configuration{}, "", e
	}
	control, e := json.Marshal(c)
	if e != nil {
		return mysqlstore.Configuration{}, "", errors.New("MYSQL_OPERATOR_CONFIGURATION_INVALID")
	}
	raw, e := json.Marshal(struct {
		Control string            `json:"control_sha256"`
		Files   map[string]string `json:"files"`
	}{SHA256(control), files})
	if e != nil {
		return mysqlstore.Configuration{}, "", errors.New("MYSQL_OPERATOR_CONFIGURATION_INVALID")
	}
	return settings, SHA256(raw), nil
}
func NewFresh(ctx context.Context, kernel *mysqlstore.Store, connection *sql.Conn, c config.Config, catalog mysqlmigrate.Catalog, options mysqlmigrate.Options) (*Fresh, error) {
	if ctx == nil || kernel == nil || kernel.Pool() == nil || connection == nil {
		return nil, errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	settings, inputs, e := freshInputs(kernel, c)
	if e != nil {
		return nil, e
	}
	if e = mysqlmigrate.VerifyInstallationLock(ctx, connection, catalog, options); e != nil {
		return nil, e
	}
	t, id, e := freshTarget(ctx, connection)
	if e != nil {
		return nil, e
	}
	f := &Fresh{kernel: kernel, connection: connection, configuration: c, catalog: catalog, options: options, settings: settings, target: t, connectionID: id, inputs: inputs}
	if e = f.Verify(ctx); e != nil {
		return nil, e
	}
	return f, nil
}
func (f *Fresh) Verify(ctx context.Context) error {
	if ctx == nil || f == nil || f.kernel == nil || f.kernel.Pool() == nil || f.connection == nil {
		return errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	settings, inputs, e := freshInputs(f.kernel, f.configuration)
	if e != nil || settings != f.settings || inputs != f.inputs {
		return errors.New("MYSQL_OPERATOR_PROTECTED_INPUT_CHANGED")
	}
	if _, e = f.kernel.Check(ctx); e != nil {
		return e
	}
	if e = mysqlmigrate.VerifyInstallationLock(ctx, f.connection, f.catalog, f.options); e != nil {
		return e
	}
	t, id, e := freshTarget(ctx, f.connection)
	if e != nil || t != f.target || id != f.connectionID || t.Database != settings.Database || t.Version != settings.ExpectedVersion || !strings.HasPrefix(t.Principal, settings.User+"@") {
		return errors.New("MYSQL_OPERATOR_TARGET_MISMATCH")
	}
	pooled, _, e := freshTarget(ctx, f.kernel.Pool())
	if e != nil || pooled != t {
		return errors.New("MYSQL_OPERATOR_POOL_TARGET_MISMATCH")
	}
	return nil
}
func (f *Fresh) VerifyConfiguration(ctx context.Context, c config.Config) error {
	if e := f.Verify(ctx); e != nil {
		return e
	}
	raw, e := json.Marshal(c)
	expected, ee := json.Marshal(f.configuration)
	if e != nil || ee != nil || SHA256(raw) != SHA256(expected) {
		return errors.New("MYSQL_OPERATOR_CONFIGURATION_MISMATCH")
	}
	return nil
}
func (f *Fresh) Binding(ctx context.Context) (string, error) {
	if e := f.Verify(ctx); e != nil {
		return "", e
	}
	raw, e := json.Marshal(struct {
		Target FreshTarget `json:"target"`
		Inputs string      `json:"inputs_sha256"`
	}{f.target, f.inputs})
	if e != nil {
		return "", errors.New("MYSQL_OPERATOR_CONFIGURATION_INVALID")
	}
	return SHA256(raw), nil
}
func (f *Fresh) Lease(ctx context.Context) (*sql.Conn, error) {
	if e := f.Verify(ctx); e != nil {
		return nil, e
	}
	return f.connection, nil
}
func (f *Fresh) Settings(ctx context.Context) (mysqlstore.Configuration, error) {
	if e := f.Verify(ctx); e != nil {
		return mysqlstore.Configuration{}, e
	}
	return f.settings, nil
}
func (f *Fresh) VerifyConnectorHashes(ctx context.Context, c mysqlstore.Configuration, password, ca string) error {
	if e := f.Verify(ctx); e != nil {
		return e
	}
	if c != f.settings || !f.kernel.InputMatches(c, password, ca) {
		return errors.New("MYSQL_OPERATOR_CONNECTOR_INPUT_MISMATCH")
	}
	return nil
}
func (f *Fresh) RequireEmpty(ctx context.Context) error {
	if e := f.Verify(ctx); e != nil {
		return e
	}
	var objects int
	e := f.connection.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE())+(SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=DATABASE())+(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA=DATABASE())+(SELECT COUNT(*) FROM information_schema.EVENTS WHERE EVENT_SCHEMA=DATABASE())`).Scan(&objects)
	if e != nil || objects != 0 {
		return errors.New("MYSQL_RESTORE_FRESH_DATABASE_REQUIRED")
	}
	return f.Verify(ctx)
}

// Promote accepts only the complete actual schema/trigger metadata and journal
// after import. It performs no DDL, migration, repair or inferred activation.
func (f *Fresh) Promote(ctx context.Context) (*Context, error) {
	if e := f.Verify(ctx); e != nil {
		return nil, e
	}
	if _, e := mysqlmigrate.VerifyInstallationSession(ctx, f.connection, f.catalog, f.options); e != nil {
		return nil, e
	}
	return New(ctx, f.kernel, f.connection, f.configuration, f.catalog, f.options)
}
