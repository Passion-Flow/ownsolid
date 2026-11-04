package middleware

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// CapturedInputs binds the bytes actually used for this connection. A later
// restored pathname cannot change the authority of an already opened connector.
type CapturedInputs struct {
	profile       Profile
	password      []byte
	ca            []byte
	operator      []byte
	backupRequest []byte
	targets       map[string][]byte
	fingerprint   string
	mysql         *mysqlstore.Configuration
}

func Capture(p Profile) (*CapturedInputs, error) {
	if e := p.Validate(); e != nil {
		return nil, e
	}
	if p.Engine == "mysql" {
		return captureMySQL(p)
	}
	password, e := readPrivate(p.PasswordFile)
	if e != nil {
		return nil, e
	}
	ca, e := readPrivate(p.CAFile)
	if e != nil {
		return nil, e
	}
	passwordInput := append([]byte(nil), password...)
	password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	if len(password) == 0 || len(password) > 4096 || strings.ContainsAny(string(password), "\x00\r\n") {
		return nil, fail("MIDDLEWARE_PASSWORD_INPUT_INVALID")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fail("MIDDLEWARE_CA_INPUT_INVALID")
	}
	c := &CapturedInputs{profile: p, password: password, ca: ca, targets: map[string][]byte{}}
	if p.Kind == platformOwned {
		c.operator, e = readPrivate(p.OperatorConfigurationFile)
		if e != nil {
			return nil, e
		}
		c.backupRequest, e = readPrivate(p.BackupRequestFile)
		if e != nil {
			return nil, e
		}
		for _, t := range p.RestoreTargets {
			c.targets[t.Key], e = readPrivate(t.OperatorConfigurationFile)
			if e != nil {
				return nil, e
			}
		}
	}
	bound, _ := json.Marshal(struct {
		Profile                               Profile
		Password, CA, Operator, BackupRequest []byte
		Targets                               map[string][]byte
	}{p, passwordInput, ca, c.operator, c.backupRequest, c.targets})
	c.fingerprint = hash(bound)
	return c, nil
}

func (c *CapturedInputs) Matches() bool {
	if c == nil {
		return false
	}
	n, e := Capture(c.profile)
	return e == nil && n.fingerprint == c.fingerprint
}

func (c *CapturedInputs) connection(seconds int) (*pgx.ConnConfig, error) {
	if c == nil || c.mysql != nil || !c.Matches() {
		return nil, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	// pgx imports libpq environment before explicit URI overrides. Refuse ambient
	// driver inputs rather than reading a user's service, pass or certificate file.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PG") {
			return nil, fail("MIDDLEWARE_AMBIENT_DRIVER_INPUT_REFUSED")
		}
	}
	u := url.URL{Scheme: "postgresql", Host: net.JoinHostPort(c.profile.Host, strconv.Itoa(int(c.profile.Port))), Path: "/" + c.profile.Database, User: url.UserPassword(c.profile.User, "explicit_input")}
	u.RawQuery = (url.Values{"sslmode": {"disable"}, "passfile": {"/dev/null"}, "sslcert": {""}, "sslkey": {""}, "sslrootcert": {""}}).Encode()
	pc, e := pgx.ParseConfig(u.String())
	if e != nil {
		return nil, fail("MIDDLEWARE_CONNECTION_CONFIGURATION_INVALID")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(c.ca) {
		return nil, fail("MIDDLEWARE_CA_INPUT_INVALID")
	}
	pc.Password = string(c.password)
	pc.TLSConfig = &tls.Config{RootCAs: pool, ServerName: c.profile.Host, MinVersion: tls.VersionTLS12}
	pc.Fallbacks = nil
	pc.ConnectTimeout = time.Duration(seconds) * time.Second
	pc.RuntimeParams = map[string]string{"application_name": "projectone_middleware_probe", "timezone": "UTC", "statement_timeout": strconv.Itoa(seconds * 1000), "lock_timeout": strconv.Itoa(seconds * 1000), "default_transaction_read_only": "on", "search_path": "pg_catalog"}
	return pc, nil
}

// Probe performs only bounded read-only SQL against the exact captured endpoint.
// It never creates a role, grants, migrates, restarts or attempts a write probe.
func Probe(ctx context.Context, c *CapturedInputs, policy Policy) (Observation, error) {
	if ctx == nil || policy.Validate() != nil || c == nil {
		return Observation{}, fail("MIDDLEWARE_CONFIGURATION_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.ProbeSeconds)*time.Second)
	defer cancel()
	pc, e := c.connection(policy.ProbeSeconds)
	if e != nil {
		return Observation{}, e
	}
	conn, e := pgx.ConnectConfig(ctx, pc)
	if e != nil {
		return Observation{}, fail("MIDDLEWARE_CONNECTION_FAILED")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(policy.ProbeSeconds)*time.Second)
		defer stop()
		_ = conn.Close(cleanup)
	}()
	secure, ok := conn.PgConn().Conn().(*tls.Conn)
	if !ok || !secure.ConnectionState().HandshakeComplete || len(secure.ConnectionState().VerifiedChains) == 0 {
		return Observation{}, fail("MIDDLEWARE_VERIFIED_TLS_REQUIRED")
	}
	tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return Observation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	defer tx.Rollback(ctx)
	var out Observation
	e = tx.QueryRow(ctx, `SELECT clock_timestamp(),current_database(),session_user,COALESCE(inet_server_addr()::text,''),inet_server_port(),current_setting('server_version_num')::integer,pg_is_in_recovery(),r.rolsuper,r.rolcreaterole,r.rolcreatedb,has_database_privilege(current_database(),'CREATE') FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&out.ObservedAt, &out.Database, &out.User, &out.ServerAddress, &out.ServerPort, &out.VersionNumber, &out.InRecovery, &out.Superuser, &out.CreateRole, &out.CreateDatabase, &out.CreateSchema)
	if e != nil || out.Database != c.profile.Database || out.User != c.profile.User || out.VersionNumber/10000 != c.profile.ExpectedMajor {
		return Observation{}, fail("MIDDLEWARE_ENDPOINT_IDENTITY_MISMATCH")
	}
	var ssl bool
	e = tx.QueryRow(ctx, `SELECT ssl,version,cipher FROM pg_catalog.pg_stat_ssl WHERE pid=pg_backend_pid()`).Scan(&ssl, &out.TLSVersion, &out.TLSCipher)
	if e != nil || !ssl || out.TLSVersion == "" || out.TLSCipher == "" {
		return Observation{}, fail("MIDDLEWARE_VERIFIED_TLS_REQUIRED")
	}
	e = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(has_table_privilege(c.oid,'SELECT')),true),COALESCE(bool_or(has_table_privilege(c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')),false) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN('r','p','m','f') AND n.nspname !~ '^pg_' AND n.nspname<>'information_schema'`).Scan(&out.TableCount, &out.ReadAllTables, &out.WriteAnyTable)
	if e != nil {
		return Observation{}, fail("MIDDLEWARE_PRIVILEGE_OBSERVATION_FAILED")
	}
	e = tx.QueryRow(ctx, `SELECT COALESCE(bool_or(has_schema_privilege(n.oid,'CREATE')),false) FROM pg_catalog.pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema'`).Scan(&out.CreateAnySchemaObjects)
	if e != nil {
		return Observation{}, fail("MIDDLEWARE_PRIVILEGE_OBSERVATION_FAILED")
	}
	if c.profile.Kind == platformOwned {
		var complete bool
		e = tx.QueryRow(ctx, `SELECT tenant_id::text,bootstrap_complete FROM public.installation_state WHERE id=1`).Scan(&out.PlatformTenantID, &complete)
		if e != nil || !complete || out.PlatformTenantID != c.profile.ExpectedTenantID {
			return Observation{}, fail("MIDDLEWARE_INSTALLED_PLATFORM_REQUIRED")
		}
	}
	if !c.Matches() {
		return Observation{}, fail("MIDDLEWARE_INPUT_CHANGED")
	}
	if !out.valid() {
		return Observation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	if e = tx.Commit(ctx); e != nil {
		return Observation{}, fail("MIDDLEWARE_PROBE_FAILED")
	}
	return out, nil
}
