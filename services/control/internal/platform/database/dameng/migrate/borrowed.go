//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"database/sql"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// CheckCatalogWithin borrows an already admitted owner connection. The caller
// retains the connection and its maintenance lock throughout. This readback
// never acquires/releases a lock, starts/ends a transaction, closes the loan,
// grants privileges or promotes catalog readiness into platform readiness.
func CheckCatalogWithin(ctx context.Context, connection *sql.Conn, configuration dameng.SchemaConfiguration) (Report, error) {
	r := Report{Scope: Scope}
	if ctx == nil {
		return r, fail("DAMENG_SCHEMA_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return r, fail("DAMENG_SCHEMA_DEADLINE_REQUIRED")
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if connection == nil || configuration.Validate() != nil {
		return r, fail("DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH")
	}
	c, err := defaultCatalog()
	if err != nil {
		return r, err
	}
	r.TotalSteps = len(c.steps)
	if configuration.MaximumMetadataRows < len(c.steps) {
		return r, fail("DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(configuration.TotalSeconds)*time.Second)
	defer cancel()
	n := &nativeBackend{connection, configuration, c}
	if err := n.ownerSession(ctx); err != nil {
		return r, err
	}
	if err := n.expressionCapabilities(ctx); err != nil {
		return r, err
	}
	if err := n.metadataCapabilities(ctx); err != nil {
		return r, err
	}
	return run(ctx, c, n, Readiness)
}
