package databaseaccess

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type mysqlAccountState struct {
	exists, locked bool
	authenticator  string
	grants         map[string]bool
}
type mysqlAccountAttribute struct {
	IntentID     string `json:"projectone_runtime_intent"`
	IntentSHA256 string `json:"intent_sha256"`
}

const maximumMySQLAuthenticatorBytes = 512
const maximumMySQLAttributeBytes = 1024

func mysqlTables() []string {
	names := append([]string{}, applicationTables[:]...)
	// The MySQL chain keeps its log table; PostgreSQL dropped it in migration 32.
	names = append(names, "model_publications", "component_log_snapshots")
	names = append(names, mysqlVariantTables[:]...)
	sort.Strings(names)
	return names
}
func mysqlExpectedGrants() map[string]bool {
	result := map[string]bool{"schema_migrations:SELECT": true}
	for _, name := range mysqlTables() {
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
			result[name+":"+privilege] = true
		}
	}
	return result
}
func mysqlAccount(i MySQLRuntimeInput) (string, error) {
	if i.connection.Validate() != nil || i.configuration.Database.MySQLRuntimeAccess == nil || i.configuration.Database.MySQLRuntimeAccess.Validate() != nil {
		return "", errors.New("MYSQL_RUNTIME_ACCESS_INPUT_REQUIRED")
	}
	return "'" + i.connection.User + "'@'" + i.configuration.Database.MySQLRuntimeAccess.AccountHost + "'", nil
}
func mysqlUserCount(ctx context.Context, c *sql.Conn, user string) (int, error) {
	var count int
	if c.QueryRowContext(ctx, "SELECT COUNT(*) FROM mysql.user WHERE User=?", user).Scan(&count) != nil {
		return 0, errors.New("MYSQL_RUNTIME_PRIVILEGED_OPERATOR_REQUIRED")
	}
	return count, nil
}

func mysqlInspect(ctx context.Context, c *sql.Conn, i MySQLRuntimeInput, intentID, intentSHA string) (mysqlAccountState, error) {
	if e := mysqlRequirePrivilegeVisibility(ctx, c); e != nil {
		return mysqlAccountState{}, e
	}
	bad := func() (mysqlAccountState, error) {
		return mysqlAccountState{}, errors.New("MYSQL_RUNTIME_ACCOUNT_OR_PRIVILEGE_CONFLICT")
	}
	account, e := mysqlAccount(i)
	if e != nil {
		return mysqlAccountState{}, e
	}
	n, e := mysqlUserCount(ctx, c, i.connection.User)
	if e != nil {
		return mysqlAccountState{}, e
	}
	if n == 0 {
		return mysqlAccountState{}, nil
	}
	if n != 1 {
		return bad()
	}
	host := i.configuration.Database.MySQLRuntimeAccess.AccountHost
	var plugin, authenticator, locked string
	var tls, unexpired, noExtraAuthentication bool
	var maximum, questions, updates, connections, lifetime, authenticatorBytes int
	e = c.QueryRowContext(ctx, `SELECT plugin,OCTET_LENGTH(authentication_string),IF(OCTET_LENGTH(authentication_string)<=?,authentication_string,''),ssl_type='ANY' AND OCTET_LENGTH(ssl_cipher)=0 AND OCTET_LENGTH(x509_issuer)=0 AND OCTET_LENGTH(x509_subject)=0,max_questions,max_updates,max_connections,max_user_connections,account_locked,password_expired='N',COALESCE(password_lifetime,-1),JSON_EXTRACT(User_attributes,'$.additional_password') IS NULL AND JSON_EXTRACT(User_attributes,'$.multi_factor_authentication') IS NULL FROM mysql.user WHERE User=? AND Host=?`, maximumMySQLAuthenticatorBytes, i.connection.User, host).Scan(&plugin, &authenticatorBytes, &authenticator, &tls, &questions, &updates, &connections, &maximum, &locked, &unexpired, &lifetime, &noExtraAuthentication)
	if e != nil || plugin != "caching_sha2_password" || authenticatorBytes < 1 || authenticatorBytes > maximumMySQLAuthenticatorBytes || !strings.HasPrefix(authenticator, "$A$") || !tls || questions != 0 || updates != 0 || connections != 0 || maximum != i.connection.Policy.MaximumOpenConnections || locked != "Y" && locked != "N" || !unexpired || lifetime != 0 || !noExtraAuthentication {
		return bad()
	}
	var attributes string
	var attributeBytes int
	e = c.QueryRowContext(ctx, `SELECT OCTET_LENGTH(CAST(ATTRIBUTE AS CHAR)),IF(OCTET_LENGTH(CAST(ATTRIBUTE AS CHAR))<=?,CAST(ATTRIBUTE AS CHAR),'') FROM information_schema.USER_ATTRIBUTES WHERE USER=? AND HOST=?`, maximumMySQLAttributeBytes, i.connection.User, host).Scan(&attributeBytes, &attributes)
	var attribute mysqlAccountAttribute
	if e != nil || attributeBytes < 1 || attributeBytes > maximumMySQLAttributeBytes || strict.DecodeStrict([]byte(attributes), &attribute) != nil || attribute.IntentID != intentID || attribute.IntentSHA256 != intentSHA {
		return bad()
	}
	var noMandatoryRoles bool
	if c.QueryRowContext(ctx, "SELECT @@global.mandatory_roles='' ").Scan(&noMandatoryRoles) != nil || !noMandatoryRoles {
		return bad()
	}
	queries := []struct {
		sql  string
		args []any
	}{
		{"SELECT COUNT(*) FROM information_schema.USER_PRIVILEGES WHERE GRANTEE=? AND (PRIVILEGE_TYPE<>'USAGE' OR IS_GRANTABLE<>'NO')", []any{account}},
		{"SELECT COUNT(*) FROM mysql.db WHERE User=? AND Host=?", []any{i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.columns_priv WHERE User=? AND Host=?", []any{i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.procs_priv WHERE User=? AND Host=?", []any{i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.global_grants WHERE USER=? AND HOST=?", []any{i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.role_edges WHERE (FROM_USER=? AND FROM_HOST=?) OR (TO_USER=? AND TO_HOST=?)", []any{i.connection.User, host, i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.default_roles WHERE (USER=? AND HOST=?) OR (DEFAULT_ROLE_USER=? AND DEFAULT_ROLE_HOST=?)", []any{i.connection.User, host, i.connection.User, host}},
		{"SELECT COUNT(*) FROM mysql.proxies_priv WHERE (User=? AND Host=?) OR (Proxied_user=? AND Proxied_host=?)", []any{i.connection.User, host, i.connection.User, host}},
	}
	for _, query := range queries {
		var extra int
		if c.QueryRowContext(ctx, query.sql, query.args...).Scan(&extra) != nil || extra != 0 {
			return bad()
		}
	}
	expected := mysqlExpectedGrants()
	seen := map[string]bool{}
	rows, e := c.QueryContext(ctx, "SELECT TABLE_SCHEMA,TABLE_NAME,PRIVILEGE_TYPE,IS_GRANTABLE FROM information_schema.TABLE_PRIVILEGES WHERE GRANTEE=? ORDER BY TABLE_SCHEMA,TABLE_NAME,PRIVILEGE_TYPE", account)
	if e != nil {
		return bad()
	}
	for rows.Next() {
		var database, table, privilege, grantable string
		if rows.Scan(&database, &table, &privilege, &grantable) != nil {
			rows.Close()
			return bad()
		}
		key := table + ":" + privilege
		if database != i.connection.Database || !expected[key] || seen[key] || grantable != "NO" {
			rows.Close()
			return bad()
		}
		seen[key] = true
	}
	e = rows.Err()
	ce := rows.Close()
	if e != nil || ce != nil {
		return bad()
	}
	return mysqlAccountState{true, locked == "Y", mysqloperator.SHA256([]byte(authenticator)), seen}, nil
}

func mysqlCheckTables(ctx context.Context, c *sql.Conn, i MySQLRuntimeInput) error {
	names := append(mysqlTables(), "schema_migrations")
	placeholders := make([]string, len(names))
	args := []any{i.connection.Database}
	for n, name := range names {
		placeholders[n] = "?"
		args = append(args, name)
	}
	rows, e := c.QueryContext(ctx, "SELECT TABLE_NAME,ENGINE,TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME IN ("+strings.Join(placeholders, ",")+")", args...)
	if e != nil {
		return errors.New("MYSQL_RUNTIME_TABLE_CATALOG_UNAVAILABLE")
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name, engine, kind string
		if rows.Scan(&name, &engine, &kind) != nil || engine != "InnoDB" || kind != "BASE TABLE" || seen[name] {
			return errors.New("MYSQL_RUNTIME_TABLE_CATALOG_INCOMPATIBLE")
		}
		seen[name] = true
	}
	if rows.Err() != nil || len(seen) != len(names) {
		return errors.New("MYSQL_RUNTIME_TABLE_CATALOG_INCOMPATIBLE")
	}
	return nil
}
