package databaseaccess

import (
	"encoding/json"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// MySQLRuntimeInput has one existing credential/connection authority. Never emit
// this object, a native option file, or its private inputs into diagnostics.
type MySQLRuntimeInput struct {
	path          string
	configuration config.Config
	connection    mysqlstore.Configuration
	password      string
	caSHA256      string
	fingerprint   string
}

func (MySQLRuntimeInput) String() string   { return "MYSQL_RUNTIME_INPUT_REDACTED" }
func (MySQLRuntimeInput) GoString() string { return "MYSQL_RUNTIME_INPUT_REDACTED" }

func LoadMySQLRuntime(path string) (MySQLRuntimeInput, error) {
	raw, e := mysqloperator.ReadPrivateBytes(path, config.MaximumProtectedConfigurationBytes)
	if e != nil {
		return MySQLRuntimeInput{}, e
	}
	c, e := config.Decode(raw)
	if e != nil {
		return MySQLRuntimeInput{}, e
	}
	if c.DatabaseEngine() != config.MySQLDatabaseEngine || c.Database.MySQLRuntimeAccess == nil {
		return MySQLRuntimeInput{}, errors.New("MYSQL_RUNTIME_ACCESS_CONFIGURATION_REQUIRED")
	}
	connectionRaw, e := mysqloperator.ReadPrivateBytes(c.Database.MySQLConfigurationFile, mysqlstore.MaximumConfigurationBytes)
	if e != nil {
		return MySQLRuntimeInput{}, e
	}
	settings, e := mysqlstore.Decode(connectionRaw, nil)
	if e != nil {
		return MySQLRuntimeInput{}, e
	}
	for _, input := range []string{path, c.Database.MySQLConfigurationFile, settings.PasswordFile, settings.CAFile} {
		if c.Database.MySQLRuntimeAccess.IntentFile == input || c.Database.MySQLRuntimeAccess.IntentFile+".created" == input {
			return MySQLRuntimeInput{}, errors.New("MYSQL_RUNTIME_ACCESS_INTENT_INPUT_OVERLAP")
		}
	}
	files := map[string]string{path: mysqloperator.SHA256(raw), c.Database.MySQLConfigurationFile: mysqloperator.SHA256(connectionRaw)}
	var password string
	for _, item := range []struct {
		path    string
		maximum int64
	}{{settings.PasswordFile, mysqlstore.MaximumPasswordBytes}, {settings.CAFile, mysqlstore.MaximumCABytes}} {
		value, e := mysqloperator.ReadPrivateBytes(item.path, item.maximum)
		if e != nil {
			return MySQLRuntimeInput{}, e
		}
		files[item.path] = mysqloperator.SHA256(value)
		if item.path == settings.PasswordFile {
			password = string(value)
		}
	}
	if !credentialValid(password) {
		return MySQLRuntimeInput{}, errors.New("RUNTIME_CREDENTIAL_FORMAT_INVALID")
	}
	projection, e := json.Marshal(struct {
		Settings mysqlstore.Configuration `json:"settings"`
		Files    map[string]string        `json:"files"`
	}{settings, files})
	if e != nil {
		return MySQLRuntimeInput{}, errors.New("MYSQL_RUNTIME_ACCESS_INPUT_INVALID")
	}
	return MySQLRuntimeInput{path: path, configuration: c, connection: settings, password: password, caSHA256: files[settings.CAFile], fingerprint: mysqloperator.SHA256(projection)}, nil
}
func (i MySQLRuntimeInput) verify() error {
	if i.path == "" || i.fingerprint == "" || !credentialValid(i.password) || i.configuration.Database.MySQLRuntimeAccess == nil {
		return errors.New("MYSQL_RUNTIME_ACCESS_INPUT_REQUIRED")
	}
	current, e := LoadMySQLRuntime(i.path)
	if e != nil || current.fingerprint != i.fingerprint {
		return errors.New("MYSQL_RUNTIME_ACCESS_INPUT_CHANGED")
	}
	return nil
}
