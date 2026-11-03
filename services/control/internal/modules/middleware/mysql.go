package middleware

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// MySQLObservation deliberately carries MySQL facts, not inferred PostgreSQL
// privilege/recovery flags. VisibleTableCount is not a complete schema inventory.
type MySQLObservation struct {
	ObservedAt        time.Time `json:"observed_at"`
	Database          string    `json:"database"`
	User              string    `json:"user"`
	ServerUUID        string    `json:"server_uuid"`
	Version           string    `json:"version"`
	TLSVersion        string    `json:"tls_version"`
	TLSCipher         string    `json:"tls_cipher"`
	ReadOnly          bool      `json:"read_only"`
	SuperReadOnly     bool      `json:"super_read_only"`
	VisibleTableCount int64     `json:"visible_table_count"`
}

// Missing false-valued facts are not equivalent to an actual false observation.
func (o *MySQLObservation) UnmarshalJSON(raw []byte) error {
	type plain MySQLObservation
	var decoded plain
	var members map[string]json.RawMessage
	if wire.StrictJSON(raw, &decoded) != nil || json.Unmarshal(raw, &members) != nil || len(members) != 10 || !MySQLObservation(decoded).valid() {
		*o = MySQLObservation{}
		return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
	}
	*o = MySQLObservation(decoded)
	return nil
}

var mysqlObservedDatabase = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var mysqlObservedUser = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}$`)
var mysqlObservedVersion = regexp.MustCompile(`^8\.4\.(0|[1-9][0-9]*)$`)

func (o MySQLObservation) valid() bool {
	return !o.ObservedAt.IsZero() && mysqlObservedDatabase.MatchString(o.Database) && mysqlObservedUser.MatchString(o.User) && validID(o.ServerUUID) && mysqlObservedVersion.MatchString(o.Version) && (o.TLSVersion == "TLSv1.2" || o.TLSVersion == "TLSv1.3") && len(o.TLSCipher) > 0 && len(o.TLSCipher) <= 100 && !strings.ContainsAny(o.TLSCipher, "\x00\r\n") && o.VisibleTableCount >= 0 && o.VisibleTableCount <= 9007199254740991
}

func captureMySQL(p Profile) (*CapturedInputs, error) {
	raw, e := readPrivate(p.MySQLConfigurationFile)
	if e != nil {
		return nil, e
	}
	var environment []byte
	if p.MySQLEnvironmentFile != "" {
		environment, e = readPrivate(p.MySQLEnvironmentFile)
		if e != nil {
			return nil, e
		}
	}
	if int64(len(raw)) > mysqlstore.MaximumConfigurationBytes || int64(len(environment)) > mysqlstore.MaximumConfigurationBytes {
		return nil, fail("MIDDLEWARE_CONNECTION_CONFIGURATION_INVALID")
	}
	settings, e := mysqlstore.Decode(raw, environment)
	if e != nil {
		return nil, fail("MIDDLEWARE_CONNECTION_CONFIGURATION_INVALID")
	}
	password, e := readPrivate(settings.PasswordFile)
	if e != nil {
		return nil, e
	}
	ca, e := readPrivate(settings.CAFile)
	if e != nil {
		return nil, e
	}
	// Preserve the existing MySQL owner's exact byte semantics; do not trim a
	// trailing newline into a different accepted credential as PostgreSQL does.
	if int64(len(password)) > mysqlstore.MaximumPasswordBytes || !utf8.Valid(password) || strings.ContainsAny(string(password), "\x00\r\n") {
		return nil, fail("MIDDLEWARE_PASSWORD_INPUT_INVALID")
	}
	if int64(len(ca)) > mysqlstore.MaximumCABytes || !x509.NewCertPool().AppendCertsFromPEM(ca) {
		return nil, fail("MIDDLEWARE_CA_INPUT_INVALID")
	}
	bound, e := json.Marshal(struct {
		Profile                                  Profile
		Configuration, Environment, Password, CA []byte
	}{p, raw, environment, password, ca})
	if e != nil {
		return nil, fail("MIDDLEWARE_CONNECTION_CONFIGURATION_INVALID")
	}
	return &CapturedInputs{profile: p, password: password, ca: ca, mysql: &settings, fingerprint: hash(bound)}, nil
}

// ProbeMySQL borrows the existing pinned TLS/authentication/session owner. It
// never migrates a database, changes grants, executes a write probe or claims
// native backup/restore support for an external instance.
func ProbeMySQL(ctx context.Context, c *CapturedInputs, policy Policy) (out MySQLObservation, result error) {
	if ctx == nil || policy.Validate() != nil || c == nil || c.mysql == nil || c.profile.Engine != "mysql" || c.profile.Kind != CustomerExternal {
		return MySQLObservation{}, fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	if !c.Matches() {
		return MySQLObservation{}, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.ProbeSeconds)*time.Second)
	defer cancel()
	store, _, e := mysqlstore.Open(ctx, *c.mysql)
	if e != nil {
		return MySQLObservation{}, fail("MIDDLEWARE_CONNECTION_FAILED")
	}
	defer func() {
		if e := store.Close(); e != nil {
			out = MySQLObservation{}
			if result == nil {
				result = fail("MIDDLEWARE_PROBE_FAILED")
			}
		}
	}()
	// A pre-open file check alone cannot bind what a racing connector read.
	if !store.InputMatches(*c.mysql, hash(c.password), hash(c.ca)) || !c.Matches() {
		return MySQLObservation{}, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	conn, e := store.Pool().Conn(ctx)
	if e != nil {
		return MySQLObservation{}, fail("MIDDLEWARE_CONNECTION_FAILED")
	}
	defer conn.Close()
	tx, e := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: true})
	if e != nil {
		return MySQLObservation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	defer tx.Rollback()
	e = tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6),DATABASE(),SUBSTRING_INDEX(CURRENT_USER(),'@',1),@@server_uuid,VERSION(),@@global.read_only,@@global.super_read_only`).Scan(&out.ObservedAt, &out.Database, &out.User, &out.ServerUUID, &out.Version, &out.ReadOnly, &out.SuperReadOnly)
	if e != nil || out.Database != c.mysql.Database || out.User != c.mysql.User || out.ServerUUID != c.profile.ExpectedMySQLServerUUID || out.Version != c.mysql.ExpectedVersion {
		return MySQLObservation{}, fail("MIDDLEWARE_ENDPOINT_IDENTITY_MISMATCH")
	}
	for _, field := range []struct {
		name, query string
		target      *string
	}{{"Ssl_version", "SHOW SESSION STATUS LIKE 'Ssl_version'", &out.TLSVersion}, {"Ssl_cipher", "SHOW SESSION STATUS LIKE 'Ssl_cipher'", &out.TLSCipher}} {
		var name string
		if e = tx.QueryRowContext(ctx, field.query).Scan(&name, field.target); e != nil || name != field.name || *field.target == "" {
			return MySQLObservation{}, fail("MIDDLEWARE_VERIFIED_TLS_REQUIRED")
		}
	}
	if out.TLSVersion != "TLSv1.2" && out.TLSVersion != "TLSv1.3" {
		return MySQLObservation{}, fail("MIDDLEWARE_VERIFIED_TLS_REQUIRED")
	}
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&out.VisibleTableCount); e != nil || out.VisibleTableCount < 0 {
		return MySQLObservation{}, fail("MIDDLEWARE_PRIVILEGE_OBSERVATION_FAILED")
	}
	if !c.Matches() {
		return MySQLObservation{}, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	if e = tx.Commit(); e != nil || ctx.Err() != nil {
		return MySQLObservation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	out.ObservedAt = out.ObservedAt.UTC()
	if !out.valid() {
		return MySQLObservation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	return out, nil
}
