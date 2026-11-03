package mysqlstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	driver "github.com/go-sql-driver/mysql"
)

const ConnectionCharset = "utf8mb4"
const ConnectionCollation = "utf8mb4_0900_bin"
const TransactionIsolation = "READ-COMMITTED"
const strictSQLModes = "ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION"

// SessionSQLModes is the fixed execution context for owned stored programs.
const SessionSQLModes = strictSQLModes

type privateDriverLogger struct{}

func (privateDriverLogger) Print(...any) { slog.Warn("MYSQL_DRIVER_DIAGNOSTIC_REDACTED") }

type Store struct {
	pool          *sql.DB
	configuration Configuration
	inputs        inputBinding
}
type Observation struct {
	Version          string `json:"version"`
	Charset          string `json:"charset"`
	Collation        string `json:"collation"`
	Isolation        string `json:"isolation"`
	TLSAuthenticated bool   `json:"tls_authenticated"`
	Scope            string `json:"scope"`
}

func connectorConfiguration(c Configuration) (*driver.Config, error) {
	configuration, _, e := connectorConfigurationAndInputs(c)
	return configuration, e
}

func connectorConfigurationAndInputs(c Configuration) (*driver.Config, inputBinding, error) {
	if e := c.Validate(); e != nil {
		return nil, inputBinding{}, e
	}
	password, e := privateInput(c.PasswordFile, MaximumPasswordBytes)
	if e != nil {
		return nil, inputBinding{}, e
	}
	if !utf8.Valid(password) || strings.ContainsAny(string(password), "\x00\r\n") {
		return nil, inputBinding{}, errors.New("MYSQL_PASSWORD_FILE_INVALID")
	}
	ca, e := privateInput(c.CAFile, MaximumCABytes)
	if e != nil {
		return nil, inputBinding{}, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, inputBinding{}, errors.New("MYSQL_CA_INVALID")
	}
	config := driver.NewConfig()
	config.Net = "tcp"
	config.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	config.User = c.User
	config.Passwd = string(password)
	config.DBName = c.Database
	config.TLS = &tls.Config{RootCAs: roots, ServerName: c.ServerName, MinVersion: tls.VersionTLS12}
	config.AllowNativePasswords = false
	config.AllowOldPasswords = false
	config.AllowCleartextPasswords = false
	config.AllowAllFiles = false
	config.AllowFallbackToPlaintext = false
	config.MultiStatements = false
	config.InterpolateParams = false
	config.CheckConnLiveness = true
	config.ParseTime = true
	config.Loc = time.UTC
	config.MaxAllowedPacket = c.Policy.MaximumPacketBytes
	config.Timeout = time.Duration(c.Policy.ConnectSeconds) * time.Second
	config.ReadTimeout = time.Duration(c.Policy.ReadSeconds) * time.Second
	config.WriteTimeout = time.Duration(c.Policy.WriteSeconds) * time.Second
	config.Logger = privateDriverLogger{}
	config.Params = map[string]string{"collation_connection": "'" + ConnectionCollation + "'", "time_zone": "'+00:00'", "transaction_isolation": "'" + TransactionIsolation + "'", "sql_mode": "'" + strictSQLModes + "'", "innodb_lock_wait_timeout": strconv.Itoa(c.Policy.LockSeconds), "max_execution_time": strconv.Itoa(c.Policy.StatementSeconds * 1000)}
	// The pinned driver's handshake collation field is one byte and rejects
	// this >255 collation even with Charset. SET NAMES establishes UTF8MB4;
	// handleParams sets the fixed binary collation before returning a connection.
	// Check verifies the actual invariant on the same authenticated connection.
	if e = config.Apply(driver.Charset(ConnectionCharset, "")); e != nil {
		return nil, inputBinding{}, errors.New("MYSQL_CHARSET_INVALID")
	}
	return config, inputBinding{passwordSHA256: inputSHA256(password), caSHA256: inputSHA256(ca)}, nil
}

func Open(ctx context.Context, c Configuration) (*Store, Observation, error) {
	config, inputs, e := connectorConfigurationAndInputs(c)
	if e != nil {
		return nil, Observation{}, e
	}
	connector, e := driver.NewConnector(config)
	if e != nil {
		return nil, Observation{}, errors.New("MYSQL_CONNECTOR_INVALID")
	}
	pool := sql.OpenDB(connector)
	pool.SetMaxOpenConns(c.Policy.MaximumOpenConnections)
	pool.SetMaxIdleConns(c.Policy.MaximumIdleConnections)
	pool.SetConnMaxLifetime(time.Duration(c.Policy.MaximumLifetimeSeconds) * time.Second)
	pool.SetConnMaxIdleTime(time.Duration(c.Policy.MaximumIdleSeconds) * time.Second)
	store := &Store{pool: pool, configuration: c, inputs: inputs}
	observed, e := store.Check(ctx)
	if e != nil {
		pool.Close()
		return nil, Observation{}, e
	}
	return store, observed, nil
}

func (s *Store) Close() error       { return s.pool.Close() }
func (s *Store) Pool() *sql.DB      { return s.pool }
func (s *Store) Stats() sql.DBStats { return s.pool.Stats() }

func (s *Store) Check(ctx context.Context) (Observation, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.Policy.ConnectSeconds)*time.Second)
	defer cancel()
	connection, e := s.pool.Conn(ctx)
	if e != nil {
		return Observation{}, SafeError(e)
	}
	defer connection.Close()
	var version, database, zone, isolation, charset, collation, modes string
	if e = connection.QueryRowContext(ctx, "SELECT VERSION(),DATABASE(),@@session.time_zone,@@session.transaction_isolation,@@session.character_set_connection,@@session.collation_connection,@@session.sql_mode").Scan(&version, &database, &zone, &isolation, &charset, &collation, &modes); e != nil {
		return Observation{}, SafeError(e)
	}
	var name, cipher string
	if e = connection.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &cipher); e != nil {
		return Observation{}, SafeError(e)
	}
	actualModes := map[string]bool{}
	for _, mode := range strings.Split(modes, ",") {
		actualModes[mode] = true
	}
	valid := version == s.configuration.ExpectedVersion && database == s.configuration.Database && zone == "+00:00" && isolation == TransactionIsolation && charset == ConnectionCharset && collation == ConnectionCollation && name == "Ssl_cipher" && cipher != ""
	for _, mode := range strings.Split(strictSQLModes, ",") {
		valid = valid && actualModes[mode]
	}
	if !valid {
		return Observation{}, errors.New("MYSQL_SESSION_INVARIANT_INVALID")
	}
	return Observation{version, charset, collation, isolation, true, "connection_and_transaction_kernel_only_not_product_backend"}, nil
}
