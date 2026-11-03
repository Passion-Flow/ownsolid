//go:build repository_integration

package nodes_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

type nodeReadWireFault interface {
	Arm(commitfault.Mode) error
	Commits() int32
	Outcomes() <-chan commitfault.Observation
	Close(context.Context) error
}

func nodeReadFaultStore(t *testing.T, x nodeReadActual) (transaction.Store, nodeReadWireFault) {
	t.Helper()
	closeProxy := func(proxy nodeReadWireFault) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if e := proxy.Close(ctx); e != nil {
				t.Error("bounded actual node read proxy cleanup", e)
			}
		})
	}
	if x.f.Store.Dialect() == transaction.PostgreSQL {
		c := x.f.PostgreSQL.Config()
		if c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 || c.ConnConfig.Host != "127.0.0.1" {
			t.Fatal("only existing explicit plaintext loopback PG fixture allowed;never downgrade TLS")
		}
		proxy, e := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
		if e != nil {
			t.Fatal(e)
		}
		closeProxy(proxy)
		proxied := c.Copy()
		proxied.ConnConfig.Port, proxied.MaxConns = proxy.Port(), 2
		pool, e := pgxpool.NewWithConfig(x.ctx, proxied)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(pool.Close)
		if e = pool.Ping(x.ctx); e != nil {
			t.Fatal("actual node read forwarding startup", e)
		}
		store, e := transaction.FromPostgreSQL(pool)
		if e != nil {
			t.Fatal(e)
		}
		return store, proxy
	}
	if x.f.Store.Dialect() != transaction.MySQL {
		t.Fatal("explicit actual paired engine required")
	}
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if e != nil {
		t.Fatal(e)
	}
	proxy, e := commitfault.NewMySQL(c)
	if e != nil {
		t.Fatal(e)
	}
	closeProxy(proxy)
	c.Port = proxy.Port()
	c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 2, 0
	kernel, observation, e := mysqlstore.Open(x.ctx, c)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("actual verified TLS node read forwarding required", e)
	}
	t.Cleanup(func() {
		if e := kernel.Close(); e != nil {
			t.Error("actual node read forwarding pool cleanup", e)
		}
	})
	store, e := transaction.FromMySQL(kernel)
	if e != nil {
		t.Fatal(e)
	}
	return store, proxy
}
func nodeReadStoredDigest(t *testing.T, x nodeReadActual) string {
	t.Helper()
	digest := sha256.New()
	queries := [][2]string{
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,name,status,version,encode(credential_hash,'hex'),protocol_version,facts,last_heartbeat_at,created_at,updated_at,execution_profile,capabilities,inference_fenced_at)::text FROM managed_nodes ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,name,status,version,HEX(credential_hash),protocol_version,facts,last_heartbeat_at,created_at,updated_at,execution_profile,capabilities,inference_fenced_at) FROM managed_nodes ORDER BY id`},
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,node_name,encode(token_hash,'hex'),created_by::text,created_at,expires_at,consumed_at,revoked_at,execution_profile,node_id::text)::text FROM node_join_tokens ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_name,HEX(token_hash),created_by,created_at,expires_at,consumed_at,revoked_at,execution_profile,node_id) FROM node_join_tokens ORDER BY id`},
		{`SELECT jsonb_build_array(id::text,tenant_id::text,workspace_id::text,node_id::text,operation,state,idempotency_key::text,requested_by::text,attempt,encode(lease_token_hash,'hex'),lease_expires_at,error_code,encode(completed_lease_hash,'hex'),encode(result_hash,'hex'),created_at,updated_at,deadline_at,completed_at,payload,encode(request_fingerprint,'hex'),inference_requested_epoch)::text FROM node_tasks ORDER BY id`, `SELECT JSON_ARRAY(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,HEX(lease_token_hash),lease_expires_at,error_code,HEX(completed_lease_hash),HEX(result_hash),created_at,updated_at,deadline_at,completed_at,payload,HEX(request_fingerprint),inference_requested_epoch) FROM node_tasks ORDER BY id`},
	}
	for _, query := range queries {
		rows, e := x.f.Store.Query(x.ctx, x.f.Statement(query[0], query[1]))
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			digest.Write(raw)
			digest.Write([]byte{'\n'})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	digest.Write([]byte(strconv.Itoa(x.audits(t))))
	return hex.EncodeToString(digest.Sum(nil))
}
func TestActualNodeReadCommitAcknowledgementLoss(t *testing.T) {
	for _, operation := range []string{"get", "list", "tokens", "tasks"} {
		t.Run(operation, func(t *testing.T) {
			for _, fault := range []struct {
				name      string
				mode      commitfault.Mode
				committed bool
			}{
				{"normal_acknowledged_commit", commitfault.Pass, true},
				{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
				{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
			} {
				t.Run(fault.name, func(t *testing.T) {
					x := newNodeReadActual(t)
					before := nodeReadStoredDigest(t, x)
					store, proxy := nodeReadFaultStore(t, x)
					originalDB, originalStorage := x.owner.DB, x.owner.Storage
					x.owner.DB, x.owner.Storage = nil, store
					x.s.DB, x.s.Workspaces.DB = nil, nil
					restore := func() {
						x.owner.DB, x.owner.Storage = originalDB, originalStorage
						x.s.DB, x.s.Workspaces.DB = originalDB, originalDB
					}
					defer restore()
					if fault.mode != commitfault.Pass {
						if e := proxy.Arm(fault.mode); e != nil {
							t.Fatal(e)
						}
					}
					var value nodes.Node
					var catalog nodes.Page[nodes.Node]
					var tokens nodes.Page[nodes.JoinToken]
					var tasks nodes.Page[nodes.Task]
					var e error
					switch operation {
					case "get":
						value, e = x.s.Get(x.ctx, x.admin, x.f.Workspace, x.ids[0])
					case "list":
						catalog, e = x.s.List(x.ctx, x.admin, x.f.Workspace, nil)
					case "tokens":
						tokens, e = x.s.Tokens(x.ctx, x.admin, nil)
					case "tasks":
						tasks, e = x.s.Tasks(x.ctx, x.admin, x.f.Workspace, x.ids[0], nil)
					}
					restore()
					if fault.mode == commitfault.Pass {
						if e != nil {
							t.Fatal("actual acknowledged node read failed", e)
						}
						switch operation {
						case "get":
							if value.ID != x.ids[0] {
								t.Fatal("acknowledged node DTO missing")
							}
						case "list":
							if len(catalog.Items) != 9 {
								t.Fatal("acknowledged node catalog missing")
							}
						case "tokens":
							if len(tokens.Items) != 9 {
								t.Fatal("acknowledged token catalog missing")
							}
						case "tasks":
							if len(tasks.Items) != 9 {
								t.Fatal("acknowledged task catalog missing")
							}
						}
					} else {
						nodeReadWant(t, e, string(transaction.CommitUnknown))
						if !reflect.DeepEqual(value, nodes.Node{}) || !reflect.DeepEqual(catalog, nodes.Page[nodes.Node]{}) || !reflect.DeepEqual(tokens, nodes.Page[nodes.JoinToken]{}) || !reflect.DeepEqual(tasks, nodes.Page[nodes.Task]{}) {
							t.Fatal("unknown read COMMIT exposed private DTO/page/cursor")
						}
						select {
						case observed := <-proxy.Outcomes():
							if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
								t.Fatal("actual node read COMMIT fault not established", observed)
							}
						case <-x.ctx.Done():
							t.Fatal("actual node read COMMIT observation missing")
						}
					}
					if proxy.Commits() != 1 {
						t.Fatal("node read COMMIT automatically resent", proxy.Commits())
					}
					if nodeReadStoredDigest(t, x) != before {
						t.Fatal("read-only transaction changed node/token/task/audit persistence")
					}
					// Recovery is a deliberate new request on the independent original
					// store,after direct inspection of the real protocol outcome.
					if recovered, e := x.s.Get(x.ctx, x.admin, x.f.Workspace, x.ids[0]); e != nil || recovered.ID != x.ids[0] {
						t.Fatal("explicit node read recovery failed", e)
					}
					if proxy.Commits() != 1 || nodeReadStoredDigest(t, x) != before {
						t.Fatal("explicit recovery retried proxy or changed persistent data")
					}
				})
			}
		})
	}
}
