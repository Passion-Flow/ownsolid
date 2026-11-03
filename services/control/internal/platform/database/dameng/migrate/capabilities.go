package migrate

import (
	"context"
	"database/sql"
)

type expressionCapability struct {
	query string
	want  string
}

// Literal synthetic values exercise the exact expression forms used by the
// catalog. These are read-only probes, not hidden temporary table/function DDL.
// Malformed JSON uses JSON_VALID so successful rejection does not poison the
// connection that must subsequently take the installation lock.
func expressionCapabilities() []expressionCapability {
	return []expressionCapability{
		{`SELECT CASE WHEN SF_GET_PARA_VALUE(1,'JSON_MODE') IN (0,2) THEN 1 ELSE 0 END FROM DUAL`, "1"},
		{`SELECT JSON_TYPE(TO_CLOB('{"capability":"ready"}')) FROM DUAL`, "OBJECT"},
		{`SELECT JSON_VALUE(TO_CLOB('{"capability":"ready"}'),'$.capability' ERROR ON EMPTY ERROR ON ERROR) FROM DUAL`, "ready"},
		{`SELECT JSON_VALUE(TO_CLOB('{"capability":false}'),'$.capability' ERROR ON EMPTY ERROR ON ERROR) FROM DUAL`, "false"},
		{`SELECT JSON_VALID(TO_CLOB('{"capability":1}')) FROM DUAL`, "1"},
		{`SELECT JSON_VALID(TO_CLOB('{"capability":')) FROM DUAL`, "0"},
		{`SELECT JSON_VALID(TO_CLOB('not-json')) FROM DUAL`, "0"},
		{`SELECT (SELECT COUNT(*) FROM JSON_OBJECT_KEYS(TO_CLOB('{"a":1,"b":2}'))) FROM DUAL`, "2"},
		{`SELECT JSON_CONTAINS_PATH(TO_CLOB('{"a":null,"b":false}'),'ALL','$.a','$.b') FROM DUAL`, "1"},
		{`SELECT JSON_CONTAINS_PATH(TO_CLOB('{"a":null}'),'ALL','$.a','$.b') FROM DUAL`, "0"},
		{`SELECT JSON_CONTAINS_PATH(TO_CLOB('[]'),'ONE','$[11]') FROM DUAL`, "0"},
		{`SELECT JSON_CONTAINS_PATH(TO_CLOB('[0,0,0,0,0,0,0,0,0,0,null]'),'ONE','$[11]') FROM DUAL`, "0"},
		{`SELECT JSON_CONTAINS_PATH(TO_CLOB('[0,0,0,0,0,0,0,0,0,0,0,null]'),'ONE','$[11]') FROM DUAL`, "1"},
		{`SELECT DBMS_LOB.COMPARE(TO_CLOB('capability-equal'),TO_CLOB('capability-equal')) FROM DUAL`, "0"},
		{`SELECT CASE WHEN DBMS_LOB.COMPARE(TO_CLOB('capability-a'),TO_CLOB('capability-b'))<>0 THEN 1 ELSE 0 END FROM DUAL`, "1"},
		{`SELECT CASE WHEN DBMS_LOB.COMPARE(TO_CLOB('capability'),TO_CLOB('capability-extra'))<>0 THEN 1 ELSE 0 END FROM DUAL`, "1"},
	}
}

func checkExpressionCapabilities(ctx context.Context, scalar func(context.Context, string) (sql.NullString, error)) error {
	if ctx == nil || scalar == nil {
		return fail("DAMENG_SCHEMA_EXPRESSION_CAPABILITY_UNAVAILABLE")
	}
	for _, check := range expressionCapabilities() {
		if ctx.Err() != nil {
			return fail("DAMENG_SCHEMA_EXPRESSION_CAPABILITY_UNAVAILABLE")
		}
		value, e := scalar(ctx, check.query)
		if e != nil || !value.Valid || value.String != check.want {
			// No secondary SQL after a native error and no server-mode/grant fixup.
			return fail("DAMENG_SCHEMA_EXPRESSION_CAPABILITY_UNAVAILABLE")
		}
	}
	return nil
}
