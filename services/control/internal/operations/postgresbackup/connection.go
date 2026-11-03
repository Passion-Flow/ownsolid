package postgresbackup

import (
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type connection struct {
	host, port, database, user, password, sslmode, ca, certificate, key string
	connectSeconds                                                      int
}

// The first native adapter deliberately admits only the equivalent one-endpoint
// URI subset. Unsupported driver features fail before allocation; they never
// fall back to different libpq or pgx transport semantics.
func readConnection(path string, r release.PostgreSQLBackupRequest, selected *pgx.ConnConfig) (connection, error) {
	bad := errors.New("POSTGRES_BACKUP_EXPLICIT_CONNECTION_PROFILE_REQUIRED")
	raw, e := readPrivateBytes(path, 16<<10)
	if e != nil {
		return connection{}, e
	}
	text := strings.TrimSpace(string(raw))
	u, e := url.Parse(text)
	if e != nil || u == nil || u.Scheme != "postgres" && u.Scheme != "postgresql" || u.User == nil || u.Hostname() == "" || u.Port() == "" || u.Fragment != "" || u.Opaque != "" || u.ForceQuery || u.RawPath != "" || strings.ContainsAny(u.Host, "%,") || strings.ContainsFunc(text, unicode.IsControl) {
		return connection{}, bad
	}
	password, ok := u.User.Password()
	port, pe := strconv.ParseUint(u.Port(), 10, 16)
	if !ok || password == "" || len(password) > 4096 || strings.ContainsAny(password, "\x00\r\n") || pe != nil || port == 0 || strconv.FormatUint(port, 10) != u.Port() || strings.TrimPrefix(u.Path, "/") != r.Database || u.User.Username() != r.Operator || strings.Contains(u.Hostname(), "/") {
		return connection{}, bad
	}
	if ip := net.ParseIP(u.Hostname()); ip == nil && strings.ContainsAny(u.Hostname(), " \\=") {
		return connection{}, bad
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return connection{}, bad
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" && key != "sslcert" && key != "sslkey" {
			return connection{}, bad
		}
		switch key {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout", "application_name", "statement_timeout", "lock_timeout", "timezone", "search_path":
		default:
			return connection{}, bad
		}
	}
	mode := q.Get("sslmode")
	if mode != "disable" && mode != "verify-full" {
		return connection{}, bad
	}
	if mode == "disable" && (q.Has("sslrootcert") || q.Has("sslcert") || q.Has("sslkey")) {
		return connection{}, bad
	}
	if mode == "verify-full" && (!release.PostgreSQLBackupPath(q.Get("sslrootcert")) || !q.Has("sslcert") || !q.Has("sslkey") || (q.Get("sslcert") == "") != (q.Get("sslkey") == "")) {
		return connection{}, bad
	}
	for _, key := range []string{"sslrootcert", "sslcert", "sslkey"} {
		if q.Get(key) != "" {
			if !release.PostgreSQLBackupPath(q.Get(key)) {
				return connection{}, bad
			}
			if _, e = readPrivateBytes(q.Get(key), 1<<20); e != nil {
				return connection{}, e
			}
		}
	}
	if q.Has("search_path") && q.Get("search_path") != "public" || q.Has("timezone") && q.Get("timezone") != "UTC" {
		return connection{}, bad
	}
	for _, key := range []string{"statement_timeout", "lock_timeout"} {
		if q.Has(key) {
			n, e := strconv.ParseUint(q.Get(key), 10, 32)
			if e != nil || strconv.FormatUint(n, 10) != q.Get(key) {
				return connection{}, bad
			}
		}
	}
	seconds := r.Policy.OperationSeconds
	if q.Has("connect_timeout") {
		n, e := strconv.Atoi(q.Get("connect_timeout"))
		if e != nil || n < 1 || n > r.Policy.OperationSeconds || strconv.Itoa(n) != q.Get("connect_timeout") {
			return connection{}, bad
		}
		seconds = n
	}
	if selected != nil && (selected.Host != u.Hostname() || selected.Port != uint16(port) || selected.Database != r.Database || selected.User != r.Operator || selected.Password != password || len(selected.Fallbacks) > 0) {
		return connection{}, bad
	}
	return connection{u.Hostname(), u.Port(), r.Database, r.Operator, password, mode, q.Get("sslrootcert"), q.Get("sslcert"), q.Get("sslkey"), seconds}, nil
}

// AdmitConnection must run before opening the selected SQL owner, so pgx cannot
// discover a service, passfile, alternate endpoint or home-derived TLS input
// from an unsupported operator URI before the native adapter refuses it.
func AdmitConnection(path string, r release.PostgreSQLBackupRequest) error {
	if !r.Valid() {
		return errors.New("POSTGRES_BACKUP_REQUEST_INVALID")
	}
	if e := RefuseAmbient(); e != nil {
		return e
	}
	_, e := readConnection(path, r, nil)
	return e
}

func passValue(s string) string { return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(s) }

func (c connection) withFiles(ctx context.Context, r release.PostgreSQLBackupRequest, run func([]string) error) (result error) {
	parent, e := openPrivateRoot(r.Policy.ScratchRoot)
	if e != nil {
		return e
	}
	defer parent.Close()
	name := "connection-" + r.BackupID
	if parent.Mkdir(name, 0700) != nil {
		return errors.New("POSTGRES_BACKUP_CONNECTION_SCOPE_EXISTS")
	}
	if e = syncRoot(parent); e != nil {
		return e
	}
	path := filepath.Join(r.Policy.ScratchRoot, name)
	root, e := openPrivateRoot(path)
	if e != nil {
		return e
	}
	created := []string{}
	defer func() {
		for i := len(created) - 1; i >= 0; i-- {
			if e := root.Remove(created[i]); e != nil {
				result = errors.Join(result, errors.New("POSTGRES_BACKUP_SECRET_CLEANUP_UNCONFIRMED"))
			}
		}
		result = errors.Join(result, syncRoot(root), root.Close())
		if result == nil || len(created) == 2 {
			if e := parent.Remove(name); e != nil {
				result = errors.Join(result, errors.New("POSTGRES_BACKUP_SECRET_CLEANUP_UNCONFIRMED"))
			}
			result = errors.Join(result, syncRoot(parent))
		}
	}()
	pass := strings.Join([]string{passValue(c.host), c.port, passValue(c.database), passValue(c.user), passValue(c.password)}, ":") + "\n"
	if e = writeNew(root, "pgpass", []byte(pass)); e != nil {
		return e
	}
	created = append(created, "pgpass")
	certificate, key := c.certificate, c.key
	if certificate == "" {
		certificate = filepath.Join(path, "no-client-certificate")
		key = filepath.Join(path, "no-client-key")
	}
	settings := [][2]string{{"host", c.host}, {"port", c.port}, {"dbname", c.database}, {"user", c.user}, {"passfile", filepath.Join(path, "pgpass")}, {"sslmode", c.sslmode}, {"sslrootcert", c.ca}, {"sslcert", certificate}, {"sslkey", key}, {"connect_timeout", strconv.Itoa(c.connectSeconds)}, {"application_name", "projectone-m18-matched-backup"}}
	var b strings.Builder
	b.WriteString("[projectone_m18_backup]\n")
	// PostgreSQL18 service-file values are literal, not conninfo-quoted. Reject
	// trim ambiguity and the parser's fixed line bound instead of re-encoding.
	for _, s := range settings {
		if strings.ContainsAny(s[1], "\x00\r\n") || strings.TrimSpace(s[1]) != s[1] || len(s[0])+len(s[1])+2 >= 1023 {
			return errors.New("POSTGRES_BACKUP_CONNECTION_PROFILE_INVALID")
		}
		b.WriteString(s[0] + "=" + s[1] + "\n")
	}
	if e = writeNew(root, "pg_service.conf", []byte(b.String())); e != nil {
		return e
	}
	created = append(created, "pg_service.conf")
	if ctx.Err() != nil {
		return errors.New("POSTGRES_BACKUP_INTERRUPTED")
	}
	return run([]string{"PGSERVICEFILE=" + filepath.Join(path, "pg_service.conf"), "PGSERVICE=projectone_m18_backup", "PGPASSFILE=" + filepath.Join(path, "pgpass")})
}
