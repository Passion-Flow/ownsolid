package databaseaccess

import (
	"context"
	"database/sql"
	"errors"
)

// MySQL filters other accounts out of both privilege views unless the inspecting
// account has SELECT at mysql-schema or global scope. Individual grant-table
// SELECT permissions do not establish complete visibility, even after GRANT.
const mysqlPrivilegeVisibilityQuery = `SELECT
EXISTS(SELECT 1 FROM information_schema.USER_PRIVILEGES
 WHERE BINARY GRANTEE=BINARY CONCAT(CHAR(39),SUBSTRING_INDEX(CURRENT_USER(),'@',1),CHAR(39),'@',CHAR(39),SUBSTRING_INDEX(CURRENT_USER(),'@',-1),CHAR(39)) AND PRIVILEGE_TYPE='SELECT')
OR EXISTS(SELECT 1 FROM information_schema.SCHEMA_PRIVILEGES
 WHERE BINARY GRANTEE=BINARY CONCAT(CHAR(39),SUBSTRING_INDEX(CURRENT_USER(),'@',1),CHAR(39),'@',CHAR(39),SUBSTRING_INDEX(CURRENT_USER(),'@',-1),CHAR(39)) AND BINARY TABLE_SCHEMA=BINARY 'mysql' AND PRIVILEGE_TYPE='SELECT')`

func mysqlRequirePrivilegeVisibility(ctx context.Context, connection *sql.Conn) error {
	bad := errors.New("MYSQL_RUNTIME_PRIVILEGE_VISIBILITY_REQUIRED")
	if ctx == nil || connection == nil {
		return bad
	}
	var complete bool
	if connection.QueryRowContext(ctx, mysqlPrivilegeVisibilityQuery).Scan(&complete) != nil || !complete {
		return bad
	}
	return nil
}
