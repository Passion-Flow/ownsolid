package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentgateway"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

var installationUnavailable = errors.New("SERVICE_NOT_READY")

// The original pinned session is the authority. A later healthy business pool
// cannot repair or adopt a lost session. Call only while holding d.verify.
func (d *selectedDatabase) verifyPostgreSQLLeaseWithin(ctx context.Context) error {
	if d.leaseLost.Load() {
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	var owns bool
	if d.pgLease == nil || d.pgLease.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted)", database.InstallationLockID).Scan(&owns) != nil || !owns {
		d.leaseLost.Store(true)
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	return nil
}

func (d *selectedDatabase) verifyServingLease(ctx context.Context) error {
	if d == nil || d.store == nil || d.verify == nil || d.postgres == nil && d.mysql == nil {
		return errors.New("SELECTED_DATABASE_REQUIRED")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	select {
	case d.verify <- struct{}{}:
		defer func() { <-d.verify }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if d.leaseLost.Load() {
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	if d.postgres != nil {
		return d.verifyPostgreSQLLeaseWithin(ctx)
	}
	if e := mysqlmigrate.VerifyInstallationLock(ctx, d.myLease, d.catalog, d.options); e != nil {
		d.leaseLost.Store(true)
		return errors.New("INSTALLATION_LOCK_LOST")
	}
	return nil
}

// One mandatory lifecycle-owned observer also covers idle workers and already
// established streams. It only observes the original session; it never reopens
// a database, reacquires a lock, or replays an operation.
func startInstallationLeaseMonitor(g *runtimeLifecycle, d *selectedDatabase, c config.Config) error {
	if g == nil || d == nil || d.store == nil {
		return errors.New("SELECTED_DATABASE_REQUIRED")
	}
	p := c.Policy().HTTP
	if e := p.Validate(); e != nil {
		return e
	}
	if d.native != nil {
		// Preserve the native full-readiness audit in this single observer.
		g.Go("DAMENG_READINESS", func(ctx context.Context) error { return watchNativeReadiness(ctx, d, c) })
		return nil
	}
	g.Go("INSTALLATION_LEASE", func(ctx context.Context) error {
		e := auditInstallationLease(ctx, config.Seconds(p.InstallationLeaseIntervalSeconds), config.Seconds(p.RequestSeconds), d.verifyServingLease)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			d.leaseLost.Store(true)
			g.Fail(errors.New("INSTALLATION_LOCK_LOST"))
			return context.Canceled // Fail already retained the precise authority failure.
		}
		return nil
	})
	return nil
}

func auditInstallationLease(ctx context.Context, interval, timeout time.Duration, verify func(context.Context) error) error {
	if interval <= 0 || timeout <= 0 || verify == nil {
		return errors.New("INSTALLATION_LEASE_POLICY_INVALID")
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if e := ctx.Err(); e != nil {
				return e
			}
			operation, cancel := context.WithTimeout(ctx, timeout)
			e := verify(operation)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e != nil {
				return e
			}
			timer.Reset(interval)
		}
	}
}

// Keep full schema verification in readiness. Every PostgreSQL request and first
// successful header instead checks only its original pinned installation lease.
// The existing process request budget bounds this check; a disconnected client
// must not cancel the shared pinned SQL connection. This adds no retry/reacquire.
func installationLeaseMiddleware(g *runtimeLifecycle, d *selectedDatabase, p config.HTTPPolicy) func(http.Handler) http.Handler {
	admission := make(chan struct{}, p.MaxActiveRequests)
	return func(next http.Handler) http.Handler {
		if d != nil && d.postgres == nil {
			return next
		}
		check := func() error {
			if g == nil || g.Err() != nil {
				return installationUnavailable
			}
			bounded, cancel := context.WithTimeout(g.ctx, config.Seconds(p.RequestSeconds))
			defer cancel()
			if e := d.verifyServingLease(bounded); e != nil {
				if g.ctx.Err() == nil {
					if d != nil {
						d.leaseLost.Store(true)
					}
					g.Fail(errors.New("INSTALLATION_LOCK_LOST"))
				}
				return installationUnavailable
			}
			return nil
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Liveness retains its fixed process-only contract. It is not business
			// admission and does not advertise a usable selected installation.
			if r.Method == http.MethodGet && r.URL.Path == "/health/live" {
				next.ServeHTTP(w, r)
				return
			}
			// An agent session lives for hours and a long-polled claim mostly waits. Both are
			// checked when they open but hold none of the shared request slots; the gateway
			// bounds them, and a lost lease stops the process and its sessions.
			if agentgateway.Owns(r) || nodehttp.ClaimRoute(cellroute.Relative(r.URL.Path), r.Method) {
				serveInstallationLeaseResponse(w, r, p, nil, check, next)
				return
			}
			serveInstallationLeaseResponse(w, r, p, admission, check, next)
		})
	}

}
func serveInstallationLeaseResponse(w http.ResponseWriter, r *http.Request, p config.HTTPPolicy, admission chan struct{}, check func() error, next http.Handler) {
	if admission != nil {
		select {
		case admission <- struct{}{}:
			defer func() { <-admission }()
		default:
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Request-ID", uuid.NewString())
			httpserver.ProblemWithPolicy(w, w.Header().Get("X-Request-ID"), errors.New("AUTH_CAPACITY_EXCEEDED"), p)
			return
		}
	}
	refuse := func() {
		for _, name := range []string{"Content-Length", "Content-Disposition", "Content-Range", "Content-Encoding", "ETag", "Last-Modified", "Accept-Ranges", "X-Artifact-SHA256", "Set-Cookie"} {
			w.Header().Del(name)
		}
		if w.Header().Get("X-Request-ID") == "" {
			w.Header().Set("X-Request-ID", uuid.NewString())
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		httpserver.ProblemWithPolicy(w, w.Header().Get("X-Request-ID"), installationUnavailable, p)
	}
	if check() != nil {
		refuse()
		return
	}
	committed, refused := false, false
	admit := func(code int) bool {
		if refused {
			return false
		}
		if committed {
			return true
		}
		if code < 200 && code != http.StatusSwitchingProtocols {
			return true
		}
		// Preserve original failed/unknown-COMMIT responses verbatim. Never
		// overwrite reconciliation status/advice after a domain error.
		if code < 400 && check() != nil {
			refused = true
			refuse()
			return false
		}
		committed = true
		return true
	}
	implicit := func() bool {
		if committed {
			return !refused
		}
		if !admit(http.StatusOK) {
			return false
		}
		w.WriteHeader(http.StatusOK)
		return true
	}
	wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
		WriteHeader: func(write httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(code int) {
				if admit(code) {
					write(code)
				}
			}
		},
		Write: func(write httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(b []byte) (int, error) {
				if !implicit() {
					return 0, installationUnavailable
				}
				return write(b)
			}
		},
		WriteString: func(write httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
			return func(s string) (int, error) {
				if !implicit() {
					return 0, installationUnavailable
				}
				return write(s)
			}
		},
		ReadFrom: func(read httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(src io.Reader) (int64, error) {
				if !implicit() {
					return 0, installationUnavailable
				}
				return read(src)
			}
		},
		Flush: func(flush httpsnoop.FlushFunc) httpsnoop.FlushFunc {
			return func() {
				if implicit() {
					flush()
				}
			}
		},
		FlushError: func(flush httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
			return func() error {
				if !implicit() {
					return installationUnavailable
				}
				return flush()
			}
		},
		Hijack: func(hijack httpsnoop.HijackFunc) httpsnoop.HijackFunc {
			return func() (net.Conn, *bufio.ReadWriter, error) {
				if refused || check() != nil {
					if !committed && !refused {
						refused = true
						refuse()
					}
					return nil, nil, installationUnavailable
				}
				return hijack()
			}
		},
	})
	next.ServeHTTP(wrapped, r)
	if !committed && !refused {
		implicit()
	}
}
