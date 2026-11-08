package scheduling

import (
	"context"
	_ "embed"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/quota.sql
var testQuotaSQL string

func quotaDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	file := os.Getenv("OWNSOLID_SCHEDULING_TEST_DSN_FILE")
	if file == "" {
		t.Skip("set OWNSOLID_SCHEDULING_TEST_DSN_FILE to a disposable local PostgreSQL database")
	}
	data, err := bundle.ReadProtectedConfiguration(file)
	if err != nil {
		t.Fatal("read protected scheduling test configuration:", err)
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") ||
		(!filepath.IsAbs(cfg.ConnConfig.Host) && !net.ParseIP(cfg.ConnConfig.Host).IsLoopback()) ||
		len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("test database must use a local socket or loopback with no fallbacks and end in _test")
	}
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	schema := "quota_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error("drop test quota schema:", err)
		}
	})
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), testQuotaSQL); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestPostgresQuotaConcurrentUpdatesCannotOverAllocate(t *testing.T) {
	pool := quotaDatabase(t)
	type result struct {
		version int64
		changed bool
		err     error
	}
	replace := func(expected int64, nodes []QuotaNode) result {
		var r result
		r.err = pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
			var err error
			r.version, r.changed, err = ReplaceQuotaWithin(t.Context(), tx, expected, nodes)
			return err
		})
		return r
	}
	initial := quotaFixture()
	initial[0].Limits.Burst[NVIDIAGPU] = 3
	initial[4].Limits.Burst[NVIDIAGPU] = 3
	if got := replace(0, initial); got != (result{version: 1, changed: true}) {
		t.Fatalf("initial policy: %+v", got)
	}
	slices.Reverse(initial)
	if got := replace(0, initial); got != (result{version: 1}) {
		t.Fatalf("replay with permuted nodes: %+v", got)
	}
	slices.Reverse(initial)

	// Each operator saw two free GPUs. Both requests are valid against that
	// snapshot, but applying both would over-allocate their parent.
	makeUpdate := func(index int) []QuotaNode {
		nodes := quotaFixture()
		nodes[0].Limits.Burst[NVIDIAGPU] = 3
		nodes[4].Limits.Burst[NVIDIAGPU] = 3
		nodes[index].Limits.Burst[NVIDIAGPU] = 5
		return nodes
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, index := range []int{0, 4} {
		wg.Go(func() {
			<-start
			results <- replace(1, makeUpdate(index))
		})
	}
	close(start)
	wg.Wait()
	close(results)
	var wins, conflicts int
	for got := range results {
		switch {
		case got == (result{version: 2, changed: true}):
			wins++
		case errors.Is(got.err, ErrQuotaRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update outcome: %+v", got)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	if err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		version, tree, err := ReadQuotaWithin(t.Context(), tx)
		if err != nil {
			return err
		}
		if version != 2 || tree.nodes["workspace-a"].Limits.Burst[NVIDIAGPU]+tree.nodes["workspace-b"].Limits.Burst[NVIDIAGPU] != 8 {
			t.Fatalf("unexpected stored policy: revision %d, %+v", version, tree)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	combined := makeUpdate(0)
	combined[4].Limits.Burst[NVIDIAGPU] = 5
	if got := replace(2, combined); !errors.Is(got.err, ErrQuotaExceedsParent) {
		t.Fatalf("rebase onto winning policy over-allocated parent: %+v", got)
	}
}
