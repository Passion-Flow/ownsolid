package modelpreparations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Native SQL is an explicit repository-owned statement. Text parameter indexes
// identify JSON/CLOB values only; binary fingerprints and canonical BLOB bytes
// retain their types. No statement text is translated or inferred at runtime.
type preparationNativeQuery struct {
	text          string
	textArgs      []int
	pageMaximum   int
	argumentOrder []int
}

func preparationDM(text string, textArgs ...int) preparationNativeQuery {
	return preparationNativeQuery{text: text, textArgs: textArgs}
}
func preparationDMPage(text string, maximum int, textArgs ...int) preparationNativeQuery {
	return preparationNativeQuery{text: text, textArgs: textArgs, pageMaximum: maximum}
}
func (query preparationNativeQuery) order(indexes ...int) preparationNativeQuery {
	query.argumentOrder = indexes
	return query
}
func (query preparationNativeQuery) bind(args []any) (string, []any, bool) {
	bound := append([]any(nil), args...)
	for _, arg := range bound {
		if stamp, ok := arg.(time.Time); ok && !preparationNativeCursor(stamp) {
			return "", nil, false
		}
	}
	for _, index := range query.textArgs {
		if index < 0 || index >= len(bound) {
			return "", nil, false
		}
		if bound[index] == nil {
			continue
		}
		var value string
		switch v := bound[index].(type) {
		case string:
			value = v
		case []byte:
			value = string(v)
		case json.RawMessage:
			value = string(v)
		default:
			return "", nil, false
		}
		if !json.Valid([]byte(value)) {
			return "", nil, false
		}
		bound[index] = value
	}
	if query.argumentOrder != nil {
		ordered := make([]any, len(query.argumentOrder))
		for i, index := range query.argumentOrder {
			if index < 0 || index >= len(bound) {
				return "", nil, false
			}
			ordered[i] = bound[index]
		}
		bound = ordered
	}
	sql := query.text
	if query.pageMaximum != 0 {
		if query.pageMaximum < 1 || len(bound) == 0 {
			return "", nil, false
		}
		limit, ok := bound[len(bound)-1].(int)
		if !ok || limit < 1 || limit > query.pageMaximum {
			return "", nil, false
		}
		sql += " LIMIT " + strconv.Itoa(limit)
		bound = bound[:len(bound)-1]
	}
	return sql, bound, sql != ""
}
func preparationSelectedSQL(q transaction.Executor, native preparationNativeQuery, pg, my string, args ...any) transaction.Statement {
	if q.Dialect() != transaction.Dameng {
		return preparationSQL(q, pg, my, args...)
	}
	text, bound, ok := native.bind(args)
	if !ok {
		return transaction.SQL(transaction.Dameng, "")
	}
	return transaction.SQL(transaction.Dameng, text, bound...)
}
func preparationSelectedRuntimeSQL(q transaction.Executor, native preparationNativeQuery, pg, my string, pgArgs, myArgs []any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, myArgs...)
	case transaction.Dameng:
		text, bound, ok := native.bind(myArgs)
		if ok {
			return transaction.SQL(transaction.Dameng, text, bound...)
		}
	}
	return transaction.SQL(q.Dialect(), "")
}
func preparationNativeCursor(value time.Time) bool {
	return value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

// An absent installation is a business refusal. Transport/statement failures
// must retain native error metadata and may not masquerade as missing setup.
func preparationInstallationFailure(q transaction.Executor, e error) error {
	if e == nil {
		return nil
	}
	if q.Dialect() == transaction.Dameng && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	return fail("MODEL_PREPARATION_INSTALLATION_REQUIRED")
}
