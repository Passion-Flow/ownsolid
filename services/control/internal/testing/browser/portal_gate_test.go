package browser_test

import (
	"context"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This explicit test scheduling wrapper holds the real executor BEFORE its
// selected transaction, so cancellation is not competing with a SQL lock fault.
// It never supplies a SQL row, state, response DTO or successful result.
type portalScheduleStore struct {
	transaction.Store
	output   string
	mu       sync.Mutex
	consumed bool
}

func (s *portalScheduleStore) Within(ctx context.Context, write bool, fn func(context.Context, transaction.Executor) error) error {
	var pcs [64]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	tool := false
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "/workflows.(*Service).executeToolNode") {
			tool = true
		}
		if !more {
			break
		}
	}
	if tool {
		s.mu.Lock()
		_, requested := os.Stat(filepath.Join(s.output, "portal-gate.request"))
		hold := requested == nil && !s.consumed
		if hold {
			s.consumed = true
		}
		s.mu.Unlock()
		if hold {
			if e := os.WriteFile(filepath.Join(s.output, "portal-gate.held"), []byte("before_transaction_executor_held"), 0600); e != nil {
				return e
			}
			for {
				if _, e := os.Stat(filepath.Join(s.output, "portal-gate.release")); e == nil {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
	}
	return s.Store.Within(ctx, write, fn)
}
func portalBrowserGate(t *testing.T, s *identity.Service, output string) func(context.Context) error {
	t.Helper()
	pool := s.DB
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	s.DB = nil
	s.Storage = &portalScheduleStore{Store: store, output: output}
	return func(ctx context.Context) error { return database.Check(ctx, pool, s.Config.Server.Profile()) }
}
