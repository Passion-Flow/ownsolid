package scope

import (
	"encoding/base64"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresInitialPort = 5432

var restoreSQLName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Credential-bearing values stay private to the operator process. Only the
// immutable snapshot's aggregate digest enters a public binding or journal.
type restoreConnection struct {
	database, operator, password, runtimeRole, runtimePassword string
	port                                                       int
}

type restoreConfigurationEnvelope struct {
	Database struct {
		DSNFile string `json:"dsn_file"`
	} `json:"database"`
	Security struct {
		AuthHMACKeyFile string `json:"auth_hmac_key_file"`
		MailKeyringFile string `json:"mail_keyring_file"`
	} `json:"security"`
	SSO struct {
		KeyringFile string `json:"keyring_file"`
	} `json:"sso"`
}

func restoreNamedFile(files []engine.ConfigurationFile, name string) ([]byte, error) {
	var found []byte
	for _, f := range files {
		if f.Name == name {
			if found != nil {
				return nil, failure("OFFLINE_SCOPE_RESTORE_DUPLICATE_INPUT")
			}
			found = f.Contents
		}
	}
	if len(found) == 0 {
		return nil, failure("OFFLINE_SCOPE_RESTORE_REQUIRED_INPUT_MISSING")
	}
	return found, nil
}

func restoreEnvelope(files []engine.ConfigurationFile, name string) (restoreConfigurationEnvelope, error) {
	var value restoreConfigurationEnvelope
	raw, e := restoreNamedFile(files, name)
	if e != nil || wire.UniqueJSON(raw, &value) != nil || value.Database.DSNFile == "" || value.Security.AuthHMACKeyFile == "" || value.Security.MailKeyringFile == "" {
		return value, failure("OFFLINE_SCOPE_RESTORE_JSON_CONFIGURATION_REQUIRED")
	}
	return value, nil
}

func restorePayloadAt(p preparedInputs, model compose.Model, role, reference string) ([]byte, error) {
	if path.Clean(reference) != reference || strings.ContainsAny(reference, "\\\x00\r\n") {
		return nil, failure("OFFLINE_SCOPE_RESTORE_PRIVATE_REFERENCE_INVALID")
	}
	root := ""
	if role == "operator-config" {
		root = engine.DatabaseTaskDestination
	}
	if role == "control-config" {
		for _, m := range model.Services["control"].Mounts {
			if m.Source == role && m.Type == "volume" && m.ReadOnly {
				if root != "" {
					return nil, failure("OFFLINE_SCOPE_RESTORE_PRIVATE_REFERENCE_INVALID")
				}
				root = m.Target
			}
		}
	}
	prefix := root + "/private/"
	if root != "" && strings.HasPrefix(reference, prefix) && path.Base(reference) == strings.TrimPrefix(reference, prefix) {
		return restoreNamedFile(p.files[role], path.Base(reference))
	}
	return nil, failure("OFFLINE_SCOPE_RESTORE_PRIVATE_REFERENCE_INVALID")
}

func restoreURI(raw []byte) (*url.URL, int, error) {
	fail := func() (*url.URL, int, error) {
		return nil, 0, failure("OFFLINE_SCOPE_RESTORE_PRIVATE_CONNECTION_REQUIRED")
	}
	u, e := url.Parse(strings.TrimSpace(string(raw)))
	if e != nil || (u.Scheme != "postgresql" && u.Scheme != "postgres") || u.Opaque != "" || u.User == nil || u.Fragment != "" || u.RawFragment != "" || u.RawPath != "" || u.RawQuery != "sslmode=disable" || u.Hostname() != "postgres" || !restoreSQLName.MatchString(u.User.Username()) || !restoreSQLName.MatchString(strings.TrimPrefix(u.Path, "/")) {
		return fail()
	}
	password, ok := u.User.Password()
	port, e := strconv.Atoi(u.Port())
	if !ok || password == "" || len(password) > 1024 || strings.ContainsAny(password, "\x00\r\n") || e != nil || port < 1 || port > 65535 || u.Host != "postgres:"+strconv.Itoa(port) {
		return fail()
	}
	return u, port, nil
}

func restoreConnections(p preparedInputs, model compose.Model) (restoreConnection, error) {
	var result restoreConnection
	op, e := restoreEnvelope(p.files["operator-config"], "control.yaml")
	if e != nil {
		return result, e
	}
	runtime, e := restoreEnvelope(p.files["operator-config"], "runtime.yaml")
	if e != nil {
		return result, e
	}
	serving, e := restoreEnvelope(p.files["control-config"], "runtime.yaml")
	if e != nil {
		return result, e
	}
	raw, e := restorePayloadAt(p, model, "operator-config", op.Database.DSNFile)
	if e != nil {
		return result, e
	}
	u, port, e := restoreURI(raw)
	if e != nil {
		return result, e
	}
	raw, e = restorePayloadAt(p, model, "operator-config", runtime.Database.DSNFile)
	if e != nil {
		return result, e
	}
	v, runtimePort, e := restoreURI(raw)
	if e != nil {
		return result, e
	}
	servingDSN, e := restorePayloadAt(p, model, "control-config", serving.Database.DSNFile)
	if e != nil {
		return result, e
	}
	if string(raw) != string(servingDSN) {
		return result, failure("OFFLINE_SCOPE_RESTORE_RUNTIME_CONNECTION_MISMATCH")
	}
	password, _ := u.User.Password()
	runtimePassword, _ := v.User.Password()
	secret, se := base64.RawURLEncoding.DecodeString(runtimePassword)
	if runtimePort != port || u.Path != v.Path || u.User.Username() == v.User.Username() || password == runtimePassword || se != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != runtimePassword {
		return result, failure("OFFLINE_SCOPE_RESTORE_DISTINCT_RUNTIME_CREDENTIAL_REQUIRED")
	}
	pg := model.Services["postgres"]
	declaredPort := postgresInitialPort
	if len(pg.Command) > 0 {
		if len(pg.Command) != 3 || pg.Command[0] != "postgres" || pg.Command[1] != "-p" {
			return result, failure("OFFLINE_SCOPE_RESTORE_POSTGRES_COMMAND_PROFILE_REQUIRED")
		}
		declaredPort, e = strconv.Atoi(pg.Command[2])
		if e != nil || strconv.Itoa(declaredPort) != pg.Command[2] {
			return result, failure("OFFLINE_SCOPE_RESTORE_POSTGRES_COMMAND_PROFILE_REQUIRED")
		}
	}
	passwordFile := pg.Environment["POSTGRES_PASSWORD_FILE"]
	if port != declaredPort || pg.Environment["POSTGRES_DB"] != strings.TrimPrefix(u.Path, "/") || pg.Environment["POSTGRES_USER"] != u.User.Username() || passwordFile == "" {
		return result, failure("OFFLINE_SCOPE_RESTORE_POSTGRES_IDENTITY_MISMATCH")
	}
	var actualPassword []byte
	for _, m := range pg.Mounts {
		if m.Type == "volume" && m.Source == "postgres-config" && m.ReadOnly && passwordFile == m.Target+"/private/"+path.Base(passwordFile) {
			actualPassword, e = restoreNamedFile(p.files[m.Source], path.Base(passwordFile))
			if e != nil {
				return result, e
			}
		}
	}
	if len(actualPassword) == 0 || strings.TrimSpace(string(actualPassword)) != password {
		return result, failure("OFFLINE_SCOPE_RESTORE_POSTGRES_CREDENTIAL_MISMATCH")
	}
	return restoreConnection{strings.TrimPrefix(u.Path, "/"), u.User.Username(), password, v.User.Username(), runtimePassword, port}, nil
}
