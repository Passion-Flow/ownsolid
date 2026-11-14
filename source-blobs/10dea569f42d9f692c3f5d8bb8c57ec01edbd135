package mysqloperator

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// configurationInputs decodes and fingerprints one protected JSON snapshot.
// Password/CA hashes must match the bytes consumed by the actual connector.
func configurationInputs(kernel *mysqlstore.Store, c config.Config) (mysqlstore.Configuration, map[string]string, error) {
	if kernel == nil || kernel.Pool() == nil || c.DatabaseEngine() != config.MySQLDatabaseEngine || c.ValidateDatabaseSelection() != nil {
		return mysqlstore.Configuration{}, nil, errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	raw, e := ReadPrivateBytes(c.Database.MySQLConfigurationFile, mysqlstore.MaximumConfigurationBytes)
	if e != nil {
		return mysqlstore.Configuration{}, nil, e
	}
	settings, e := mysqlstore.Decode(raw, nil)
	if e != nil {
		return mysqlstore.Configuration{}, nil, e
	}
	files := map[string]string{c.Database.MySQLConfigurationFile: SHA256(raw)}
	for _, item := range []struct {
		path    string
		maximum int64
	}{{settings.PasswordFile, mysqlstore.MaximumPasswordBytes}, {settings.CAFile, mysqlstore.MaximumCABytes}} {
		value, e := ReadPrivateBytes(item.path, item.maximum)
		if e != nil {
			return mysqlstore.Configuration{}, nil, e
		}
		files[item.path] = SHA256(value)
	}
	if !kernel.InputMatches(settings, files[settings.PasswordFile], files[settings.CAFile]) {
		return mysqlstore.Configuration{}, nil, errors.New("MYSQL_OPERATOR_CONNECTOR_INPUT_MISMATCH")
	}
	return settings, files, nil
}

// VerifyConnectorInputs proves actual opened inputs and authenticated TLS/session
// invariants. This is neither installation-lock nor schema/ledger acceptance.
func VerifyConnectorInputs(ctx context.Context, kernel *mysqlstore.Store, c config.Config) error {
	if ctx == nil {
		return errors.New("MYSQL_OPERATOR_CONTEXT_REQUIRED")
	}
	if _, _, e := configurationInputs(kernel, c); e != nil {
		return e
	}
	_, e := kernel.Check(ctx)
	return e
}
