//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"sync/atomic"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// WithReadyRuntime holds the same installation lock used by schema Run through
// the complete operator callback. verify must be called before each mutation;
// it checks this still-live owner session and the actual restricted runtime.
// Neither callback nor verifier grants schema mutation or owner SQL access.
func WithReadyRuntime(ctx context.Context, owner, runtime *dameng.Store, c dameng.SchemaConfiguration, operation func(context.Context, func(context.Context) error) error) (Report, error) {
	if operation == nil || !owner.SameSchemaTarget(runtime) {
		return Report{Scope: Scope}, fail("DAMENG_OPERATOR_TARGET_REQUIRED")
	}
	return withOwner(ctx, owner, c, func(ctx context.Context, n *nativeBackend) (r Report, e error) {
		var active atomic.Bool
		active.Store(true)
		defer active.Store(false)
		verify := func(ctx context.Context) error {
			if !active.Load() {
				return fail("DAMENG_OPERATOR_CONTEXT_EXPIRED")
			}
			if _, e := run(ctx, n.catalog, n, Readiness); e != nil {
				return e
			}
			runtimeReport, e := CheckRuntime(ctx, runtime, c)
			if e != nil {
				return e
			}
			if !runtimeReport.RuntimeReady {
				return fail("DAMENG_SCHEMA_NOT_READY")
			}
			return nil
		}
		if e = verify(ctx); e != nil {
			return r, e
		}
		r = Report{Scope: Scope, TotalSteps: len(n.catalog.steps), AppliedSteps: len(n.catalog.steps), FoundationReady: true, RuntimeReady: true}
		if e = operation(ctx, verify); e != nil {
			return r, e
		}
		return r, verify(ctx)
	})
}
