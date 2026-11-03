package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestExpressionAdmissionUsesReadOnlyExactResults(t *testing.T) {
	checks := expressionCapabilities()
	calls := 0
	e := checkExpressionCapabilities(context.Background(), func(_ context.Context, query string) (sql.NullString, error) {
		if query != checks[calls].query || !strings.HasPrefix(query, "SELECT ") || !strings.HasSuffix(query, " FROM DUAL") || strings.Contains(query, ";") {
			t.Fatal("unexpected or mutating admission SQL")
		}
		want := checks[calls].want
		calls++
		return sql.NullString{String: want, Valid: true}, nil
	})
	if e != nil || calls != len(checks) {
		t.Fatal("complete capability projection refused", e)
	}
}

func TestExpressionAdmissionStopsAtMissingOrChangedCapability(t *testing.T) {
	checks := expressionCapabilities()
	for bad := range checks {
		for _, mode := range []string{"native_error", "null", "wrong_value"} {
			calls := 0
			e := checkExpressionCapabilities(context.Background(), func(_ context.Context, _ string) (sql.NullString, error) {
				index := calls
				calls++
				if index == bad {
					switch mode {
					case "native_error":
						return sql.NullString{}, errors.New("synthetic unavailable capability")
					case "null":
						return sql.NullString{}, nil
					default:
						return sql.NullString{String: "unexpected", Valid: true}, nil
					}
				}
				return sql.NullString{String: checks[index].want, Valid: true}, nil
			})
			if FailureCode(e) != "DAMENG_SCHEMA_EXPRESSION_CAPABILITY_UNAVAILABLE" || calls != bad+1 {
				t.Fatal("admission accepted mismatch or queried after failure", bad, mode)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if checkExpressionCapabilities(ctx, func(context.Context, string) (sql.NullString, error) {
		t.Fatal("SQL after cancellation")
		return sql.NullString{}, nil
	}) == nil {
		t.Fatal("cancelled admission accepted")
	}
}
