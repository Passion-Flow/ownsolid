package database

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestConnectionErrorNamesAFullConnectionLimit(t *testing.T) {
	for code, want := range map[string]string{"53300": "DATABASE_CONNECTION_LIMIT_REACHED", "28P01": "DATABASE_AUTHENTICATION_FAILED", "XX000": "DATABASE_UNAVAILABLE"} {
		if got := connectionError(&pgconn.PgError{Code: code}); got.Error() != want {
			t.Errorf("%s: %v, want %s", code, got, want)
		}
	}
}
