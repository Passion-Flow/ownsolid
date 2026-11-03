package databaseaccess

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const maximumConfigurationBytes = 256 * 1024
const maximumDSNBytes = 16 * 1024

var roleName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// RuntimeInput deliberately keeps the parsed credential-bearing connection
// private. Diagnostics return status codes, never this object or its contents.
type RuntimeInput struct {
	configuration config.Config
	connection    *pgxpool.Config
}

func readPrivate(path string, maximum int64) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("RUNTIME_ACCESS_FILE_PATH_INVALID")
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, errors.New("RUNTIME_ACCESS_FILE_PARENT_INVALID")
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, errors.New("RUNTIME_ACCESS_FILE_PARENT_INVALID")
	}
	defer r.Close()
	owned := func(i os.FileInfo, single bool) bool {
		s, ok := i.Sys().(*syscall.Stat_t)
		return ok && uint64(s.Uid) == uint64(os.Geteuid()) && (!single || s.Nlink == 1)
	}
	p, e := r.Stat(".")
	if e != nil || p.Mode() != os.ModeDir|0700 || !owned(p, false) {
		return nil, errors.New("RUNTIME_ACCESS_FILE_PARENT_INVALID")
	}
	f, e := r.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("RUNTIME_ACCESS_PRIVATE_FILE_INVALID")
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode() != 0600 || !owned(i, true) || i.Size() < 1 || i.Size() > maximum {
		return nil, errors.New("RUNTIME_ACCESS_PRIVATE_FILE_INVALID")
	}
	b, e := io.ReadAll(io.LimitReader(f, maximum+1))
	j, je := f.Stat()
	current, ce := r.Lstat(filepath.Base(path))
	if e != nil || je != nil || ce != nil || !os.SameFile(i, j) || !os.SameFile(i, current) || current.Mode() != 0600 || !owned(current, true) || j.Mode() != 0600 || !owned(j, true) || i.Size() != int64(len(b)) || j.Size() != i.Size() || current.Size() != i.Size() || !i.ModTime().Equal(j.ModTime()) || !current.ModTime().Equal(i.ModTime()) {
		return nil, errors.New("RUNTIME_ACCESS_PRIVATE_FILE_CHANGED")
	}
	return b, nil
}
func LoadRuntime(path string) (RuntimeInput, error) {
	return LoadRuntimeWithEnvironment(path, "")
}

// LoadRuntimeWithEnvironment binds runtime credentials to the exact effective
// typed configuration, using the original protected snapshot and policy parser.
// Empty environment preserves historical LoadRuntime behavior.
func LoadRuntimeWithEnvironment(path, environmentFile string) (RuntimeInput, error) {
	// pgx otherwise imports these ambient libpq settings and may discover
	// private service/pass/certificate files. This component owns one explicit
	// URI boundary and never changes the shared process or account environment.
	for _, key := range []string{"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE", "PGSERVICE", "PGSERVICEFILE", "PGSSLMODE", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGOPTIONS", "PGAPPNAME", "PGCONNECT_TIMEOUT", "PGTARGETSESSIONATTRS", "PGTZ", "PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION"} {
		if _, present := os.LookupEnv(key); present {
			return RuntimeInput{}, errors.New("RUNTIME_ACCESS_AMBIENT_DRIVER_CONFIGURATION_REFUSED")
		}
	}
	b, e := readPrivate(path, maximumConfigurationBytes)
	if e != nil {
		return RuntimeInput{}, e
	}
	c, e := config.DecodeWithEnvironment(b, environmentFile)
	if e != nil {
		return RuntimeInput{}, e
	}
	b, e = readPrivate(c.Database.DSNFile, maximumDSNBytes)
	if e != nil {
		return RuntimeInput{}, e
	}
	value := strings.TrimSpace(string(b))
	u, e := url.Parse(value)
	if e != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Hostname() == "" || u.Port() == "" || u.Path == "" || u.Path == "/" || u.Fragment != "" {
		return RuntimeInput{}, errors.New("RUNTIME_DSN_SINGLE_ENDPOINT_URI_REQUIRED")
	}
	password, present := u.User.Password()
	if !present || !credentialValid(password) || !roleName.MatchString(u.User.Username()) {
		return RuntimeInput{}, errors.New("RUNTIME_CREDENTIAL_FORMAT_INVALID")
	}
	ssl := u.Query().Get("sslmode")
	if ssl != "disable" && ssl != "require" && ssl != "verify-ca" && ssl != "verify-full" {
		return RuntimeInput{}, errors.New("RUNTIME_DSN_EXPLICIT_TRANSPORT_REQUIRED")
	}
	query := u.Query()
	allowed := map[string]bool{"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "channel_binding": true, "connect_timeout": true, "application_name": true, "statement_timeout": true, "lock_timeout": true, "timezone": true, "search_path": true}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return RuntimeInput{}, errors.New("RUNTIME_DSN_PARAMETER_UNSUPPORTED")
		}
	}
	if (query.Get("sslcert") == "") != (query.Get("sslkey") == "") || (ssl == "verify-ca" || ssl == "verify-full") && query.Get("sslrootcert") == "" {
		return RuntimeInput{}, errors.New("RUNTIME_DSN_EXPLICIT_TLS_REFERENCES_REQUIRED")
	}
	// Empty explicit references override pgx's home-derived certificate defaults.
	for _, key := range []string{"sslcert", "sslkey", "sslrootcert", "sslpassword"} {
		if _, present := query[key]; !present {
			query.Set(key, "")
		}
	}
	parsed := *u
	parsed.RawQuery = query.Encode()
	pc, e := pgxpool.ParseConfig(parsed.String())
	port, pe := strconv.ParseUint(u.Port(), 10, 16)
	if e != nil || pe != nil || port == 0 || pc.ConnConfig.Host != u.Hostname() || uint64(pc.ConnConfig.Port) != port || pc.ConnConfig.Database != strings.TrimPrefix(u.Path, "/") || pc.ConnConfig.User != u.User.Username() || pc.ConnConfig.Password != password || len(pc.ConnConfig.Fallbacks) > 0 {
		return RuntimeInput{}, errors.New("RUNTIME_DSN_CONFIG_INVALID")
	}
	pc.MaxConns = c.Database.MaxOpenConnections
	pc.MinConns = c.Database.MaxIdleConnections
	return RuntimeInput{configuration: c, connection: pc}, nil
}
