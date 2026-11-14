package mysqloperator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const targetQuery = "SELECT @@server_uuid,DATABASE(),CURRENT_USER(),VERSION(),(SELECT deployment_id FROM platform_license_state WHERE id=1)"

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var principalPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}@[A-Za-z0-9_.:%-]{1,255}$`)

type Target struct {
	Format         string `json:"format"`
	ServerUUID     string `json:"server_uuid"`
	Database       string `json:"database"`
	Principal      string `json:"principal"`
	Version        string `json:"version"`
	InstallationID string `json:"installation_id"`
}

func (t Target) valid() bool {
	return t.Format == "projectone.mysql.operator-target.v1" && uuidPattern.MatchString(t.ServerUUID) && uuidPattern.MatchString(t.InstallationID) && t.InstallationID != "00000000-0000-0000-0000-000000000000" && principalPattern.MatchString(t.Principal) && t.Database != "" && t.Version != ""
}

type row interface{ Scan(...any) error }

func readTarget(query func() row) (Target, error) {
	t := Target{Format: "projectone.mysql.operator-target.v1"}
	if e := query().Scan(&t.ServerUUID, &t.Database, &t.Principal, &t.Version, &t.InstallationID); e != nil || !t.valid() {
		return Target{}, errors.New("MYSQL_OPERATOR_TARGET_UNAVAILABLE")
	}
	return t, nil
}

// Context borrows actual owners; it cannot close/adopt a pool or installation
// connection. No client-controlled or PostgreSQL callback constructs this proof.
type Context struct {
	kernel        *mysqlstore.Store
	connection    *sql.Conn
	store         transaction.Store
	configuration config.Config
	settings      mysqlstore.Configuration
	catalog       mysqlmigrate.Catalog
	options       mysqlmigrate.Options
	target        Target
	inputs        string
}

func New(ctx context.Context, kernel *mysqlstore.Store, connection *sql.Conn, c config.Config, catalog mysqlmigrate.Catalog, options mysqlmigrate.Options) (*Context, error) {
	if ctx == nil || kernel == nil || kernel.Pool() == nil || connection == nil || c.DatabaseEngine() != config.MySQLDatabaseEngine || c.ValidateDatabaseSelection() != nil {
		return nil, errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	settings, _, e := configurationInputs(kernel, c)
	if e != nil {
		return nil, e
	}
	store, e := transaction.FromMySQL(kernel)
	if e != nil {
		return nil, e
	}
	o := &Context{kernel: kernel, connection: connection, store: store, configuration: c, settings: settings, catalog: catalog, options: options}
	if e = o.verifySession(ctx); e != nil {
		return nil, e
	}
	o.target, e = readTarget(func() row { return connection.QueryRowContext(ctx, targetQuery) })
	if e != nil {
		return nil, e
	}
	if o.target.Database != settings.Database || o.target.Version != settings.ExpectedVersion || !strings.HasPrefix(o.target.Principal, settings.User+"@") {
		return nil, errors.New("MYSQL_OPERATOR_TARGET_MISMATCH")
	}
	o.inputs, e = o.inputFingerprint()
	if e != nil {
		return nil, e
	}
	if e = o.Verify(ctx); e != nil {
		return nil, e
	}
	return o, nil
}

func (o *Context) inputFingerprint() (string, error) {
	control, e := json.Marshal(o.configuration)
	if e != nil {
		return "", errors.New("MYSQL_OPERATOR_CONFIGURATION_INVALID")
	}
	settings, files, e := configurationInputs(o.kernel, o.configuration)
	if e != nil {
		return "", e
	}
	if settings != o.settings {
		return "", errors.New("MYSQL_OPERATOR_CONNECTOR_INPUT_MISMATCH")
	}
	raw, e := json.Marshal(struct {
		Control string            `json:"control_sha256"`
		Files   map[string]string `json:"files"`
	}{SHA256(control), files})
	if e != nil {
		return "", errors.New("MYSQL_OPERATOR_CONFIGURATION_INVALID")
	}
	return SHA256(raw), nil
}
func (o *Context) verifySession(ctx context.Context) error {
	if o == nil || o.kernel == nil || o.kernel.Pool() == nil || o.connection == nil || ctx == nil {
		return errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	if _, e := mysqlmigrate.VerifyRuntimeLedger(ctx, o.connection, o.catalog, o.options); e != nil {
		return e
	}
	if _, e := o.kernel.Check(ctx); e != nil {
		return e
	}
	return nil
}
func (o *Context) Verify(ctx context.Context) error {
	if e := o.verifySession(ctx); e != nil {
		return e
	}
	leased, e := readTarget(func() row { return o.connection.QueryRowContext(ctx, targetQuery) })
	if e != nil || leased != o.target {
		return errors.New("MYSQL_OPERATOR_TARGET_MISMATCH")
	}
	pooled, e := readTarget(func() row { return o.kernel.Pool().QueryRowContext(ctx, targetQuery) })
	if e != nil || pooled != o.target {
		return errors.New("MYSQL_OPERATOR_POOL_TARGET_MISMATCH")
	}
	inputs, e := o.inputFingerprint()
	if e != nil || inputs != o.inputs {
		return errors.New("MYSQL_OPERATOR_PROTECTED_INPUT_CHANGED")
	}
	return nil
}

// VerifyConfiguration binds an operator consumer's typed roots/policies to the
// actual protected configuration already retained by this installation owner.
func (o *Context) VerifyConfiguration(ctx context.Context, c config.Config) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	actual, e := json.Marshal(c)
	expected, ee := json.Marshal(o.configuration)
	if e != nil || ee != nil || SHA256(actual) != SHA256(expected) {
		return errors.New("MYSQL_OPERATOR_CONFIGURATION_MISMATCH")
	}
	return nil
}

// VerifyConnectorHashes permits a native operator to reuse only the exact
// private credential/CA bytes consumed by this actual authenticated connector.
func (o *Context) VerifyConnectorHashes(ctx context.Context, c mysqlstore.Configuration, passwordSHA256, caSHA256 string) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	if c != o.settings || !o.kernel.InputMatches(c, passwordSHA256, caSHA256) {
		return errors.New("MYSQL_OPERATOR_CONNECTOR_INPUT_MISMATCH")
	}
	return nil
}
func (o *Context) Binding(ctx context.Context) (string, error) {
	if e := o.Verify(ctx); e != nil {
		return "", e
	}
	raw, e := json.Marshal(struct {
		Target Target `json:"target"`
		Inputs string `json:"inputs_sha256"`
	}{o.target, o.inputs})
	if e != nil {
		return "", errors.New("MYSQL_OPERATOR_TARGET_INVALID")
	}
	return SHA256(raw), nil
}
func (o *Context) VerifyIdentity(ctx context.Context, s *identity.Service) error {
	if s == nil || s.DB != nil || s.Config.DatabaseEngine() != config.MySQLDatabaseEngine {
		return errors.New("MYSQL_OPERATOR_IDENTITY_MISMATCH")
	}
	if e := o.Verify(ctx); e != nil {
		return e
	}
	control, e := json.Marshal(s.Config)
	expected, ee := json.Marshal(o.configuration)
	if e != nil || ee != nil || SHA256(control) != SHA256(expected) {
		return errors.New("MYSQL_OPERATOR_IDENTITY_MISMATCH")
	}
	store, e := s.Repository()
	if e != nil || store.Dialect() != transaction.MySQL {
		return errors.New("MYSQL_OPERATOR_IDENTITY_MISMATCH")
	}
	actual, e := readTarget(func() row { return store.QueryRow(ctx, transaction.SQL(transaction.MySQL, targetQuery)) })
	if e != nil || actual != o.target {
		return errors.New("MYSQL_OPERATOR_IDENTITY_MISMATCH")
	}
	return nil
}
func (o *Context) Target(ctx context.Context) (Target, error) {
	if e := o.Verify(ctx); e != nil {
		return Target{}, e
	}
	return o.target, nil
}
func (o *Context) Lease(ctx context.Context) (*sql.Conn, error) {
	if e := o.Verify(ctx); e != nil {
		return nil, e
	}
	return o.connection, nil
}
func (o *Context) Store(ctx context.Context) (transaction.Store, error) {
	if e := o.Verify(ctx); e != nil {
		return nil, e
	}
	return o.store, nil
}

// VerifyInstallation keeps privileged full object/trigger metadata distinct from
// runtime DML-visible ledger verification, on this same installation session.
func (o *Context) VerifyInstallation(ctx context.Context) error {
	if e := o.Verify(ctx); e != nil {
		return e
	}
	if _, e := mysqlmigrate.VerifyInstallationSession(ctx, o.connection, o.catalog, o.options); e != nil {
		return e
	}
	return o.Verify(ctx)
}
