//go:build repository_integration

package nodes_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const nodeJoinTargetMarkerKind = "projectone-node-join-target-isolated-v1"
const nodeJoinTargetPrivateBytes = 64 << 10

var errNodeJoinTargetScope = errors.New("NODE_JOIN_TARGET_PRIVATE_EMPTY_ISOLATED_SCOPE_REQUIRED")

type nodeJoinTargetMarker struct {
	Kind             string `json:"kind"`
	Engine           string `json:"engine"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	ConnectionSHA256 string `json:"connection_sha256"`
	Disposable       bool   `json:"disposable"`
}

// This test-only boundary reads an operator-owned snapshot without following
// links or exposing any credential, raw JSON, path error or vendor diagnostic.
func nodeJoinTargetPrivate(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errNodeJoinTargetScope
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, errNodeJoinTargetScope
	}
	owned := func(info os.FileInfo, directory bool) bool {
		if info == nil {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(os.Geteuid()) {
			return false
		}
		if directory {
			return info.Mode() == os.ModeDir|0700
		}
		return info.Mode() == 0600 && stat.Nlink == 1 && info.Size() > 0 && info.Size() <= nodeJoinTargetPrivateBytes
	}
	beforeParent, e := os.Lstat(parent)
	if e != nil || !owned(beforeParent, true) {
		return nil, errNodeJoinTargetScope
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, errNodeJoinTargetScope
	}
	defer root.Close()
	openedParent, e := root.Stat(".")
	if e != nil || !os.SameFile(beforeParent, openedParent) {
		return nil, errNodeJoinTargetScope
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !owned(before, false) {
		return nil, errNodeJoinTargetScope
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errNodeJoinTargetScope
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !owned(opened, false) || !os.SameFile(before, opened) {
		return nil, errNodeJoinTargetScope
	}
	raw, e := io.ReadAll(io.LimitReader(file, nodeJoinTargetPrivateBytes+1))
	after, se := file.Stat()
	current, le := root.Lstat(name)
	currentParent, pe := os.Lstat(parent)
	if e != nil || se != nil || le != nil || pe != nil || int64(len(raw)) != before.Size() || !owned(after, false) || !owned(current, false) || !owned(currentParent, true) || !os.SameFile(before, after) || !os.SameFile(before, current) || !os.SameFile(beforeParent, currentParent) || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errNodeJoinTargetScope
	}
	return raw, nil
}
func nodeJoinTargetDecode(raw []byte) (nodeJoinTargetMarker, error) {
	var value nodeJoinTargetMarker
	d := json.NewDecoder(bytes.NewReader(raw))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return value, errNodeJoinTargetScope
	}
	allowed := map[string]bool{"kind": true, "engine": true, "database": true, "username": true, "connection_sha256": true, "disposable": true}
	seen := map[string]bool{}
	for d.More() {
		token, e := d.Token()
		name, ok := token.(string)
		if e != nil || !ok || !allowed[name] || seen[name] {
			return value, errNodeJoinTargetScope
		}
		seen[name] = true
		var field json.RawMessage
		if d.Decode(&field) != nil {
			return value, errNodeJoinTargetScope
		}
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') || len(seen) != len(allowed) {
		return value, errNodeJoinTargetScope
	}
	var extra any
	if d.Decode(&extra) != io.EOF || json.Unmarshal(raw, &value) != nil {
		return value, errNodeJoinTargetScope
	}
	if value.Kind != nodeJoinTargetMarkerKind || !value.Disposable || value.Engine != "mysql" && value.Engine != "postgresql" || !regexp.MustCompile(`^m20_repository_[a-f0-9]{20}_test$`).MatchString(value.Database) || !regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`).MatchString(value.Username) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value.ConnectionSHA256) {
		return value, errNodeJoinTargetScope
	}
	return value, nil
}

type nodeJoinTargetConnection struct {
	store  transaction.Store
	close  func()
	marker nodeJoinTargetMarker
}

func nodeJoinTargetOpen(ctx context.Context, readOnly bool) (nodeJoinTargetConnection, error) {
	var result nodeJoinTargetConnection
	engine := os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE")
	path := os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION")
	if engine == "postgresql" {
		path = os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE")
	}
	raw, e := nodeJoinTargetPrivate(path)
	if e != nil {
		return result, errNodeJoinTargetScope
	}
	digest := sha256.Sum256(raw)
	result.marker = nodeJoinTargetMarker{Kind: nodeJoinTargetMarkerKind, Engine: engine, ConnectionSHA256: hex.EncodeToString(digest[:]), Disposable: true}
	validDatabase := regexp.MustCompile(`^m20_repository_[a-f0-9]{20}_test$`)
	if engine == "mysql" {
		c, e := mysqlstore.Load(path, "")
		again, re := nodeJoinTargetPrivate(path)
		if e != nil || re != nil || !bytes.Equal(raw, again) || c.Host != "127.0.0.1" || !validDatabase.MatchString(c.Database) {
			return result, errNodeJoinTargetScope
		}
		c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 1, 1
		kernel, observation, e := mysqlstore.Open(ctx, c)
		if e != nil || !observation.TLSAuthenticated {
			if kernel != nil {
				kernel.Close()
			}
			return result, errNodeJoinTargetScope
		}
		result.close = func() { kernel.Close() }
		result.store, e = transaction.FromMySQL(kernel)
		if e == nil && readOnly {
			_, e = kernel.Pool().ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY")
		}
		if e != nil {
			result.close()
			return result, errNodeJoinTargetScope
		}
		result.marker.Database, result.marker.Username = c.Database, c.User
	} else if engine == "postgresql" {
		c, e := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
		if e != nil || c.ConnConfig.Host != "127.0.0.1" || !validDatabase.MatchString(c.ConnConfig.Database) || c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 {
			return result, errNodeJoinTargetScope
		}
		mode := "off"
		if readOnly {
			mode = "on"
		}
		c.MaxConns = 1
		c.ConnConfig.RuntimeParams = map[string]string{"timezone": "UTC", "statement_timeout": "5000", "lock_timeout": "1000", "default_transaction_read_only": mode, "search_path": "public"}
		pool, e := pgxpool.NewWithConfig(ctx, c)
		if e != nil {
			return result, errNodeJoinTargetScope
		}
		result.close = pool.Close
		result.store, e = transaction.FromPostgreSQL(pool)
		if e != nil {
			result.close()
			return result, errNodeJoinTargetScope
		}
		result.marker.Database, result.marker.Username = c.ConnConfig.Database, c.ConnConfig.User
	} else {
		return result, errNodeJoinTargetScope
	}
	return result, nil
}
func nodeJoinTargetVerify(ctx context.Context, connection nodeJoinTargetConnection, marker string) error {
	raw, e := nodeJoinTargetPrivate(marker)
	if e != nil {
		return errNodeJoinTargetScope
	}
	actual, e := nodeJoinTargetDecode(raw)
	if e != nil || actual != connection.marker {
		return errNodeJoinTargetScope
	}
	var database, username string
	var relations int
	statementText := `SELECT current_database(),current_user,(SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND c.relkind IN('r','p','v','m','f','S'))`
	if connection.store.Dialect() == transaction.MySQL {
		statementText = `SELECT DATABASE(),SUBSTRING_INDEX(CURRENT_USER(),'@',1),(SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE())`
	}
	if connection.store.QueryRow(ctx, transaction.SQL(connection.store.Dialect(), statementText)).Scan(&database, &username, &relations) != nil || database != actual.Database || username != actual.Username || relations != 0 {
		return errNodeJoinTargetScope
	}
	return nil
}

// Target catalogue commands explicitly enable this guard before any fixture
// migration. This test issues metadata reads only and never adopts existing data.
func TestNodeJoinTargetEmptyScope(t *testing.T) {
	if os.Getenv("PROJECT_ONE_NODE_JOIN_TARGET_GUARD") != "1" {
		t.Skip("explicit isolated native target guard required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	connection, e := nodeJoinTargetOpen(ctx, true)
	if e != nil {
		t.Fatal(errNodeJoinTargetScope)
	}
	defer connection.close()
	if nodeJoinTargetVerify(ctx, connection, os.Getenv("PROJECT_ONE_NODE_JOIN_TARGET_MARKER")) != nil {
		t.Fatal(errNodeJoinTargetScope)
	}
	t.Log("actual_read_only_private_empty_scope_guard=true migration_started=false")
}

func TestActualNodeJoinTargetScopeGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	connection, e := nodeJoinTargetOpen(ctx, false)
	if e != nil {
		t.Fatal(errNodeJoinTargetScope)
	}
	defer connection.close()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private actual guard fixture")
	}
	marker := filepath.Join(root, "target-marker.json")
	raw, _ := json.Marshal(connection.marker)
	writeMarker := func(body []byte) {
		if os.WriteFile(marker, body, 0600) != nil || os.Chmod(marker, 0600) != nil {
			t.Fatal("private marker fixture")
		}
	}
	writeMarker(raw)
	t.Run("private_marker_and_empty_scope", func(t *testing.T) {
		if nodeJoinTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("actual empty scoped marker refused")
		}
	})
	t.Run("invalid_marker_or_path_refused", func(t *testing.T) {
		bad := connection.marker
		bad.Disposable = false
		notDisposable, _ := json.Marshal(bad)
		bad = connection.marker
		bad.Database = "m20_repository_00000000000000000000_test"
		if bad.Database == connection.marker.Database {
			bad.Database = "m20_repository_11111111111111111111_test"
		}
		foreign, _ := json.Marshal(bad)
		bad = connection.marker
		bad.ConnectionSHA256 = strings.Repeat("0", 64)
		changed, _ := json.Marshal(bad)
		for _, body := range [][]byte{nil, []byte(`{"kind":"SYNTHETIC_PRIVATE_MARKER"}`), append(append([]byte{}, raw...), []byte(` {}`)...), []byte(strings.Replace(string(raw), `"kind":`, `"kind":"duplicate","kind":`, 1)), notDisposable, foreign, changed} {
			writeMarker(body)
			if e := nodeJoinTargetVerify(ctx, connection, marker); e != errNodeJoinTargetScope {
				t.Fatal("invalid actual marker accepted or unsanitized")
			}
		}
		writeMarker(raw)
		os.Chmod(marker, 0644)
		if nodeJoinTargetVerify(ctx, connection, marker) == nil {
			t.Fatal("public marker accepted")
		}
		os.Chmod(marker, 0600)
		link := filepath.Join(root, "alias.json")
		if os.Symlink(marker, link) != nil {
			t.Fatal("symlink fixture")
		}
		if nodeJoinTargetVerify(ctx, connection, link) == nil {
			t.Fatal("symlink marker accepted")
		}
		os.Remove(link)
		if os.Link(marker, link) != nil {
			t.Fatal("hardlink fixture")
		}
		if nodeJoinTargetVerify(ctx, connection, marker) == nil {
			t.Fatal("hardlinked marker accepted")
		}
		os.Remove(link)
		if nodeJoinTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("explicit marker repair failed")
		}
	})
	t.Run("existing_relation_refused_without_migration", func(t *testing.T) {
		if nodeJoinTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("nonempty test did not start empty")
		}
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "CREATE TABLE node_join_target_guard_fixture(value integer)")); e != nil {
			t.Fatal("owned guard sentinel creation")
		}
		dropped := false
		t.Cleanup(func() {
			if !dropped {
				clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, e := connection.store.Exec(clean, transaction.SQL(connection.store.Dialect(), "DROP TABLE node_join_target_guard_fixture")); e != nil {
					t.Error("owned guard sentinel cleanup")
				}
			}
		})
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "INSERT INTO node_join_target_guard_fixture(value) VALUES(1)")); e != nil {
			t.Fatal("owned guard sentinel data")
		}
		if nodeJoinTargetVerify(ctx, connection, marker) != errNodeJoinTargetScope {
			t.Fatal("nonempty actual scope was adopted")
		}
		var count int
		if connection.store.QueryRow(ctx, transaction.SQL(connection.store.Dialect(), "SELECT count(*) FROM node_join_target_guard_fixture")).Scan(&count) != nil || count != 1 {
			t.Fatal("refused guard changed sentinel")
		}
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "DROP TABLE node_join_target_guard_fixture")); e != nil {
			t.Fatal("explicit guard fixture repair")
		}
		dropped = true
		if nodeJoinTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("explicit empty-scope recovery failed")
		}
	})
}
