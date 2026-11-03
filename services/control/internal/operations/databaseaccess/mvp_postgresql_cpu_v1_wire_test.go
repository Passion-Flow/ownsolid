package databaseaccess

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// A protocol-transparent, loopback-only fixture. The selected COMMIT reaches
// real PostgreSQL; its complete ReadyForQuery is observed, then its responses
// are lost with the original connection. No synthetic success/error substitutes
// for pgx's actual ambiguous-commit result. Credentials are forwarded, never logged.
type mvpCommitProxy struct {
	listener   net.Listener
	target     string
	armed      atomic.Bool
	suppressed atomic.Int32
	mu         sync.Mutex
	conns      map[net.Conn]bool
	workers    sync.WaitGroup
}

func newMVPCommitProxy(t *testing.T, host string, port uint16) *mvpCommitProxy {
	t.Helper()
	if host != "127.0.0.1" {
		t.Fatal("owned loopback fixture required")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:44802")
	if err != nil {
		t.Fatal(err)
	}
	p := &mvpCommitProxy{listener: listener, target: net.JoinHostPort(host, strconv.Itoa(int(port))), conns: map[net.Conn]bool{}}
	p.workers.Add(1)
	go func() {
		defer p.workers.Done()
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			p.workers.Add(1)
			go p.forward(c)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		p.mu.Lock()
		for c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
		p.workers.Wait()
	})
	return p
}
func mvpWirePacket(c net.Conn) ([]byte, error) {
	header := make([]byte, 5)
	if _, e := io.ReadFull(c, header); e != nil {
		return nil, e
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length < 4 || length > 16*1024*1024 {
		return nil, io.ErrUnexpectedEOF
	}
	packet := make([]byte, int(length)+1)
	copy(packet, header)
	_, e := io.ReadFull(c, packet[5:])
	return packet, e
}
func (p *mvpCommitProxy) forward(client net.Conn) {
	defer p.workers.Done()
	backend, err := net.DialTimeout("tcp", p.target, 3*time.Second)
	if err != nil {
		client.Close()
		return
	}
	p.mu.Lock()
	p.conns[client] = true
	p.conns[backend] = true
	p.mu.Unlock()
	defer func() {
		client.Close()
		backend.Close()
		p.mu.Lock()
		delete(p.conns, client)
		delete(p.conns, backend)
		p.mu.Unlock()
	}()
	header := make([]byte, 4)
	if _, err = io.ReadFull(client, header); err != nil {
		return
	}
	length := binary.BigEndian.Uint32(header)
	if length < 8 || length > 16384 {
		return
	}
	startup := make([]byte, length)
	copy(startup, header)
	if _, err = io.ReadFull(client, startup[4:]); err != nil {
		return
	}
	if _, err = backend.Write(startup); err != nil {
		return
	}
	var drop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		defer backend.Close()
		for {
			packet, e := mvpWirePacket(client)
			if e != nil {
				return
			}
			if packet[0] == 'Q' && strings.EqualFold(strings.TrimSpace(strings.TrimRight(string(packet[5:]), "\x00")), "commit") && p.armed.CompareAndSwap(true, false) {
				drop.Store(true)
			}
			if _, e = backend.Write(packet); e != nil {
				return
			}
		}
	}()
	for {
		packet, e := mvpWirePacket(backend)
		if e != nil {
			break
		}
		if drop.Load() {
			if packet[0] == 'Z' {
				p.suppressed.Add(1)
				break
			}
			continue
		}
		if _, e = client.Write(packet); e != nil {
			break
		}
	}
	client.Close()
	backend.Close()
	<-done
}

func TestMVPPostgreSQLCPUV1ActualCommitAcknowledgmentLoss(t *testing.T) {
	for _, phase := range []string{"schema", "grants"} {
		t.Run(phase, func(t *testing.T) {
			f := newActualFixture(t, "localization")
			ctx := t.Context()
			if _, err := Prepare(ctx, f.service.DB, f.lease, f.input); err != nil {
				t.Fatal(err)
			}
			identityfixture.Bootstrap(t, f.service)
			before := mvpCommonDataFingerprint(t, f)
			principal, err := readMVPPrincipal(ctx, f.lease, f.input)
			if err != nil {
				t.Fatal(err)
			}
			input := mvpRuntimeInput(t, f)
			cc := f.service.DB.Config().Copy()
			proxy := newMVPCommitProxy(t, cc.ConnConfig.Host, cc.ConnConfig.Port)
			cc.ConnConfig.Port = 44802
			cc.ConnConfig.RuntimeParams["statement_timeout"] = "30000"
			p, err := pgxpool.NewWithConfig(ctx, cc)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			lease, err := p.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			if _, err = f.lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID); err != nil {
				t.Fatal(err)
			}
			var locked bool
			if err = lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); err != nil || !locked {
				t.Fatal("fresh explicit fault operator lease", err)
			}
			proxied := input
			proxied.connection = input.connection.Copy()
			proxied.connection.ConnConfig.Port = 44802
			steps := mvpUpgradeSteps{migrate: database.Migrate, commit: func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) }}
			if phase == "schema" {
				steps.migrate = func(ctx context.Context, c database.MigrationConnection, profiles ...string) error {
					proxy.armed.Store(true)
					return database.Migrate(ctx, c, profiles...)
				}
			} else {
				steps.commit = func(ctx context.Context, tx pgx.Tx) error { proxy.armed.Store(true); return tx.Commit(ctx) }
			}
			status, err := upgradeMVPPostgreSQLCPUV1(ctx, p, lease, proxied, steps)
			if status != "" || err == nil || err.Error() != string(transaction.CommitUnknown) || proxy.suppressed.Load() != 1 {
				t.Fatal("real lost COMMIT must be unknown/no success", status, err, proxy.suppressed.Load())
			}
			// The original failed operator session is gone. Only this explicit new
			// operator invocation acquires a fresh lease; production never reconnects it.
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if e := f.lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil {
					t.Fatal(e)
				}
				if locked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !locked {
				t.Fatal("failed session retained installation lock")
			}
			expected := MVPPostgreSQLCPUV1GrantsPending
			if phase == "grants" {
				expected = MVPPostgreSQLCPUV1UpgradeComplete
			}
			if state, e := InspectMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || state != expected {
				t.Fatal("actual original-state readback", state, e)
			}
			var ledgerBefore, ledgerAfter string
			query := "SELECT string_agg(version::text||':'||checksum||':'||applied_at::text,',' ORDER BY version) FROM schema_migrations"
			if e := f.lease.QueryRow(ctx, query).Scan(&ledgerBefore); e != nil {
				t.Fatal(e)
			}
			if status, e := UpgradeMVPPostgreSQLCPUV1(ctx, f.service.DB, f.lease, input); e != nil || status != MVPPostgreSQLCPUV1UpgradeComplete {
				t.Fatal("explicit original-state recovery", status, e)
			}
			if e := f.lease.QueryRow(ctx, query).Scan(&ledgerAfter); e != nil || ledgerBefore != ledgerAfter {
				t.Fatal("recovery replayed committed migration", e)
			}
			after, e := readMVPPrincipal(ctx, f.lease, input)
			if e != nil || principal != after || mvpCommonDataFingerprint(t, f) != before {
				t.Fatal("lost-commit recovery changed principal/data", e)
			}
		})
	}
}
