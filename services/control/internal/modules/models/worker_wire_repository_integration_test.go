//go:build repository_integration

package models_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

type modelCommitProxy interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func modelWireConnection(t *testing.T, x humanImportFixture) (transaction.Store, modelCommitProxy, func()) {
	t.Helper()
	var proxy modelCommitProxy
	var store transaction.Store
	var closePool func()
	var closed sync.Once
	shutdown := func() {
		closed.Do(func() {
			if closePool != nil {
				closePool()
			}
			if proxy != nil {
				ctx, cancel := context.WithTimeout(context.Background(), config.Seconds(x.owner.Config.Policy().Models.CleanupSeconds))
				defer cancel()
				if e := proxy.Close(ctx); e != nil {
					t.Error("owned actual wire transport cleanup", e)
				}
			}
		})
	}
	t.Cleanup(shutdown)
	if x.f.Store.Dialect() == transaction.MySQL {
		c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
		if e != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
			t.Fatal("isolated protected MySQL wire input required")
		}
		p, e := commitfault.NewMySQL(c)
		if e != nil {
			t.Fatal(e)
		}
		proxy = p
		c.Port = p.Port()
		c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 2, 0
		kernel, observed, e := mysqlstore.Open(x.ctx, c)
		if e != nil {
			t.Fatal("actual authenticated TLS wire connector", e)
		}
		closePool = func() {
			if e := kernel.Close(); e != nil {
				t.Error("actual wire pool close", e)
			}
		}
		if !observed.TLSAuthenticated {
			t.Fatal("actual wire TLS authentication required")
		}
		store, e = transaction.FromMySQL(kernel)
		if e != nil {
			t.Fatal(e)
		}
	} else {
		if x.f.PostgreSQL == nil {
			t.Fatal("actual isolated PostgreSQL pool required")
		}
		c := x.f.PostgreSQL.Config()
		if c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 || c.ConnConfig.Host != "127.0.0.1" {
			t.Fatal("existing explicit plaintext loopback fixture required; never downgrade TLS")
		}
		p, e := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
		if e != nil {
			t.Fatal(e)
		}
		proxy = p
		c.ConnConfig.Port, c.MaxConns = p.Port(), 2
		pool, e := pgxpool.NewWithConfig(x.ctx, c)
		if e != nil {
			t.Fatal("actual PostgreSQL wire pool", e)
		}
		closePool = pool.Close
		if e = pool.Ping(x.ctx); e != nil {
			t.Fatal("actual PostgreSQL wire startup", e)
		}
		store, e = transaction.FromPostgreSQL(pool)
		if e != nil {
			t.Fatal(e)
		}
	}
	return store, proxy, shutdown
}

func TestActualModelImportWorkerWire(t *testing.T) {
	for _, name := range []string{"normal", "claim_not_forwarded", "claim_acknowledgement_lost", "publication_not_forwarded", "publication_acknowledgement_lost"} {
		t.Run(name, func(t *testing.T) {
			x := archiveSetup(t)
			v := x.start(t)
			archiveRecoveryObjects(t, x, v.ID)
			archiveQueue(t, x, v.ID)
			store, proxy, shutdown := modelWireConnection(t, x)
			owner := *x.owner
			owner.Storage = store
			worker := *x.s
			worker.Identity = &owner
			workspaceService := *x.s.Workspaces
			workspaceService.Identity = &owner
			worker.Workspaces = &workspaceService
			mode := commitfault.Pass
			if strings.HasSuffix(name, "not_forwarded") {
				mode = commitfault.DropBeforeCommit
			} else if strings.HasSuffix(name, "acknowledgement_lost") {
				mode = commitfault.DropCommitAcknowledgement
			}
			publicationFault := strings.HasPrefix(name, "publication_")
			var gate *archiveGateOwner
			if publicationFault {
				gate = &archiveGateOwner{Store: store, claimed: make(chan struct{}), release: make(chan struct{})}
				owner.Storage = gate
			} else if mode != commitfault.Pass {
				if e := proxy.Arm(mode); e != nil {
					t.Fatal(e)
				}
			}
			type result struct {
				owned bool
				err   error
			}
			done, stopped := make(chan result, 1), make(chan struct{})
			go func() {
				defer close(stopped)
				owned, e := worker.ProcessNext(x.ctx)
				done <- result{owned, e}
			}()
			var release sync.Once
			unblock := func() {
				if gate != nil {
					release.Do(func() { close(gate.release) })
				}
			}
			t.Cleanup(func() {
				unblock()
				timer := time.NewTimer(config.Seconds(x.owner.Config.Policy().Models.CleanupSeconds))
				defer timer.Stop()
				select {
				case <-stopped:
				case <-timer.C:
					t.Error("owned wire worker cleanup deadline")
				}
			})
			if publicationFault {
				select {
				case <-gate.claimed:
				case got := <-done:
					t.Fatal("worker exited before actual publication wire gate", got.err)
				case <-x.ctx.Done():
					t.Fatal("publication wire scheduling deadline")
				}
				if proxy.Commits() != 1 {
					t.Fatal("exact real claim COMMIT required before arming publication fault")
				}
				if e := proxy.Arm(mode); e != nil {
					t.Fatal(e)
				}
				unblock()
			}
			select {
			case got := <-done:
				if !got.owned {
					t.Fatal("actual wire worker did not own its import", got.err)
				}
				if mode == commitfault.Pass {
					if got.err != nil {
						t.Fatal("normal actual forwarding", got.err)
					}
				} else {
					modelReadError(t, got.err, string(transaction.CommitUnknown))
				}
			case <-x.ctx.Done():
				t.Fatal("actual worker wire outcome deadline")
			}
			if mode != commitfault.Pass {
				select {
				case observation := <-proxy.Outcomes():
					committed := mode == commitfault.DropCommitAcknowledgement
					if observation.Mode != mode || !observation.Suppressed || observation.Forwarded != committed || observation.ServerOK != committed {
						t.Fatal("actual worker COMMIT observation mismatched", observation)
					}
				case <-x.ctx.Done():
					t.Fatal("actual worker COMMIT observation missing")
				}
			}
			commits := int32(1)
			if name == "normal" || publicationFault {
				commits = 2
			}
			if proxy.Commits() != commits {
				t.Fatal("unexpected or automatically replayed actual COMMIT", proxy.Commits(), commits)
			}
			// End the old transport and physical sessions before independent
			// reads. An immutable object alone establishes no SQL outcome.
			shutdown()
			ready := name == "normal" || name == "publication_acknowledgement_lost"
			state, attempts := "verifying", 1
			if name == "claim_not_forwarded" {
				state, attempts = "queued", 0
			} else if ready {
				state = "ready"
			}
			for range 2 {
				workerState(t, x, v.ID, state, attempts, "")
				if ready {
					archiveReady(t, x, v.ID, 1)
				} else {
					archiveNoPublication(t, x, v.ID)
				}
				if x.count(t, "model.import_failed", v.ID) != 0 || x.count(t, "model.import_verification_started", v.ID) != attempts {
					t.Fatal("unknown outcome created speculative failure or duplicated claim audit")
				}
			}
			fresh := *x.s
			owned, e := fresh.ProcessNext(x.ctx)
			if e != nil || owned == ready {
				t.Fatal("fresh service did not reconcile exact durable wire outcome", owned, ready, e)
			}
			x.s = &fresh
			archiveReady(t, x, v.ID, 1)
			if e = modelartifact.VerifyDirectory(x.ctx, filepath.Join(x.root, "objects", v.ID)); e != nil {
				t.Fatal("actual wire recovery changed immutable files", e)
			}
			t.Logf("Actual worker wire: mode=%d COMMIT_commands=%d direct_checks=2 final_attempts=1; no process restart/browser/native claim", mode, commits)
		})
	}
}
