// Package coldtrace is used only by ARC's disposable diagnostic overlays.
package coldtrace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type requestKey struct{}
type queryKey struct{}
type poolKey struct{}

type request struct {
	id, stage string
	started   time.Time
}
type operation struct {
	phase   string
	started time.Time
}

var log = slog.New(slog.NewJSONHandler(os.Stderr, nil))

func event(ctx context.Context, name string, args ...any) {
	info, _ := ctx.Value(requestKey{}).(request)
	var remaining any
	if deadline, ok := ctx.Deadline(); ok {
		remaining = max(time.Until(deadline).Milliseconds(), 0)
	}
	fields := []any{"event", name, "request_id", info.id, "request_stage", info.stage, "remaining_ms", remaining}
	if ctx.Err() != nil {
		fields = append(fields, "context_state", errorKind(ctx.Err()))
	}
	log.Info("arc104_diagnostic", append(fields, args...)...)
}

func Start(ctx context.Context, id string) context.Context {
	ctx = context.WithValue(ctx, requestKey{}, request{id: id, stage: "admission", started: time.Now()})
	event(ctx, "request_start")
	return ctx
}

func Stage(ctx context.Context, stage string) context.Context {
	info, ok := ctx.Value(requestKey{}).(request)
	if !ok {
		return ctx
	}
	info.stage = stage
	ctx = context.WithValue(ctx, requestKey{}, info)
	event(ctx, "request_stage")
	return ctx
}

func End(ctx context.Context, status int, publicCode string) {
	info, ok := ctx.Value(requestKey{}).(request)
	if ok {
		event(ctx, "request_end", "http_status", status, "public_code", publicCode,
			"elapsed_ms", time.Since(info.started).Milliseconds())
	}
}

func phase(sql string) string {
	switch {
	case sql == "commit":
		return "commit"
	case strings.HasPrefix(sql, "begin"):
		return "begin"
	case sql == "rollback":
		return "rollback"
	case strings.Contains(sql, "FROM cell_cluster_members m JOIN managed_nodes"):
		return "member_select"
	case strings.Contains(sql, "SELECT EXISTS(SELECT 1 FROM cell_clusters"):
		return "cluster_visibility"
	case strings.Contains(sql, "FROM users WHERE id=") && strings.Contains(sql, "FOR NO KEY UPDATE"):
		return "authority_user_lock"
	case strings.Contains(sql, "FROM sessions WHERE id=") && strings.Contains(sql, "FOR UPDATE"):
		return "authority_session_lock"
	default:
		return "sql_step"
	}
}

func errorKind(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, pgx.ErrTxCommitRollback):
		return "commit_rolled_back"
	default:
		return "driver_error"
	}
}

func errorFields(err error) []any {
	state := ""
	var e *pgconn.PgError
	if errors.As(err, &e) && len(e.Code) == 5 && strings.IndexFunc(e.Code, func(r rune) bool {
		return !('0' <= r && r <= '9' || 'A' <= r && r <= 'Z')
	}) < 0 {
		state = e.Code
	}
	return []any{"error_kind", errorKind(err), "error_type", fmt.Sprintf("%T", err), "sqlstate", state}
}

type Tracer struct{}

func (Tracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	op := operation{phase: phase(data.SQL), started: time.Now()}
	_, tracked := ctx.Value(requestKey{}).(request)
	if !tracked && op.phase != "commit" {
		return ctx
	}
	if tracked {
		event(ctx, "sql_start", "phase", op.phase, "backend_pid", conn.PgConn().PID())
	}
	return context.WithValue(ctx, queryKey{}, op)
}

func (Tracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	op, ok := ctx.Value(queryKey{}).(operation)
	_, tracked := ctx.Value(requestKey{}).(request)
	if !ok || !tracked && data.Err == nil {
		return
	}
	fields := []any{"phase", op.phase, "backend_pid", conn.PgConn().PID(),
		"elapsed_ms", time.Since(op.started).Milliseconds()}
	if op.phase == "commit" {
		fields = append(fields, "commit_acknowledged", data.Err == nil && data.CommandTag.String() == "COMMIT")
	}
	event(ctx, "sql_end", append(fields, errorFields(data.Err)...)...)
}

func (Tracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if _, ok := ctx.Value(requestKey{}).(request); !ok {
		return ctx
	}
	event(ctx, "pool_acquire_start")
	return context.WithValue(ctx, poolKey{}, time.Now())
}

func (Tracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	started, ok := ctx.Value(poolKey{}).(time.Time)
	if !ok {
		return
	}
	var pid uint32
	if data.Conn != nil {
		pid = data.Conn.PgConn().PID()
	}
	event(ctx, "pool_acquire_end", append([]any{"backend_pid", pid, "elapsed_ms", time.Since(started).Milliseconds()}, errorFields(data.Err)...)...)
}
