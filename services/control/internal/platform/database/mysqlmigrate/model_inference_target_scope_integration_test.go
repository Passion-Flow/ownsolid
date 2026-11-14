//go:build mysql_integration

package mysqlmigrate

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

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const modelInferenceTargetMarkerKind = "projectone-model-inference-target-isolated-v1"
const modelInferenceTargetPrivateBytes = 64 << 10

var errModelInferenceTargetScope = errors.New("MODEL_INFERENCE_TARGET_PRIVATE_EMPTY_ISOLATED_SCOPE_REQUIRED")

type modelInferenceTargetMarker struct {
	Kind             string `json:"kind"`
	Engine           string `json:"engine"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	ConnectionSHA256 string `json:"connection_sha256"`
	Disposable       bool   `json:"disposable"`
}

// This test-only boundary reads an operator-owned snapshot without following
// links or exposing any credential, raw JSON, path error or vendor diagnostic.
func modelInferenceTargetPrivate(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errModelInferenceTargetScope
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, errModelInferenceTargetScope
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
		return info.Mode() == 0600 && stat.Nlink == 1 && info.Size() > 0 && info.Size() <= modelInferenceTargetPrivateBytes
	}
	beforeParent, e := os.Lstat(parent)
	if e != nil || !owned(beforeParent, true) {
		return nil, errModelInferenceTargetScope
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, errModelInferenceTargetScope
	}
	defer root.Close()
	openedParent, e := root.Stat(".")
	if e != nil || !os.SameFile(beforeParent, openedParent) {
		return nil, errModelInferenceTargetScope
	}
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !owned(before, false) {
		return nil, errModelInferenceTargetScope
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errModelInferenceTargetScope
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !owned(opened, false) || !os.SameFile(before, opened) {
		return nil, errModelInferenceTargetScope
	}
	raw, e := io.ReadAll(io.LimitReader(file, modelInferenceTargetPrivateBytes+1))
	after, se := file.Stat()
	current, le := root.Lstat(name)
	currentParent, pe := os.Lstat(parent)
	if e != nil || se != nil || le != nil || pe != nil || int64(len(raw)) != before.Size() || !owned(after, false) || !owned(current, false) || !owned(currentParent, true) || !os.SameFile(before, after) || !os.SameFile(before, current) || !os.SameFile(beforeParent, currentParent) || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errModelInferenceTargetScope
	}
	return raw, nil
}
func modelInferenceTargetDecode(raw []byte) (modelInferenceTargetMarker, error) {
	var value modelInferenceTargetMarker
	d := json.NewDecoder(bytes.NewReader(raw))
	start, e := d.Token()
	if e != nil || start != json.Delim('{') {
		return value, errModelInferenceTargetScope
	}
	allowed := map[string]bool{"kind": true, "engine": true, "database": true, "username": true, "connection_sha256": true, "disposable": true}
	seen := map[string]bool{}
	for d.More() {
		token, e := d.Token()
		name, ok := token.(string)
		if e != nil || !ok || !allowed[name] || seen[name] {
			return value, errModelInferenceTargetScope
		}
		seen[name] = true
		var field json.RawMessage
		if d.Decode(&field) != nil {
			return value, errModelInferenceTargetScope
		}
	}
	end, e := d.Token()
	if e != nil || end != json.Delim('}') || len(seen) != len(allowed) {
		return value, errModelInferenceTargetScope
	}
	var extra any
	if d.Decode(&extra) != io.EOF || json.Unmarshal(raw, &value) != nil {
		return value, errModelInferenceTargetScope
	}
	if value.Kind != modelInferenceTargetMarkerKind || !value.Disposable || value.Engine != "mysql" || !regexp.MustCompile(`^m20_migration_[a-f0-9]{20}_test$`).MatchString(value.Database) || !regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`).MatchString(value.Username) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(value.ConnectionSHA256) {
		return value, errModelInferenceTargetScope
	}
	return value, nil
}

type modelInferenceTargetConnection struct {
	store  transaction.Store
	close  func()
	marker modelInferenceTargetMarker
}

func modelInferenceTargetOpen(ctx context.Context, readOnly bool) (modelInferenceTargetConnection, error) {
	var result modelInferenceTargetConnection
	engine := "mysql"
	path := os.Getenv("PROJECT_ONE_MYSQL_MIGRATION_CONFIGURATION")
	raw, e := modelInferenceTargetPrivate(path)
	if e != nil {
		return result, errModelInferenceTargetScope
	}
	digest := sha256.Sum256(raw)
	result.marker = modelInferenceTargetMarker{Kind: modelInferenceTargetMarkerKind, Engine: engine, ConnectionSHA256: hex.EncodeToString(digest[:]), Disposable: true}
	validDatabase := regexp.MustCompile(`^m20_migration_[a-f0-9]{20}_test$`)
	if engine == "mysql" {
		c, e := mysqlstore.Load(path, "")
		again, re := modelInferenceTargetPrivate(path)
		if e != nil || re != nil || !bytes.Equal(raw, again) || c.Host != "127.0.0.1" || !validDatabase.MatchString(c.Database) {
			return result, errModelInferenceTargetScope
		}
		c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 1, 1
		kernel, observation, e := mysqlstore.Open(ctx, c)
		if e != nil || !observation.TLSAuthenticated {
			if kernel != nil {
				kernel.Close()
			}
			return result, errModelInferenceTargetScope
		}
		result.close = func() { kernel.Close() }
		result.store, e = transaction.FromMySQL(kernel)
		if e == nil && readOnly {
			_, e = kernel.Pool().ExecContext(ctx, "SET SESSION TRANSACTION READ ONLY")
		}
		if e != nil {
			result.close()
			return result, errModelInferenceTargetScope
		}
		result.marker.Database, result.marker.Username = c.Database, c.User
	} else {
		return result, errModelInferenceTargetScope
	}
	return result, nil
}
func modelInferenceTargetVerify(ctx context.Context, connection modelInferenceTargetConnection, marker string) error {
	raw, e := modelInferenceTargetPrivate(marker)
	if e != nil {
		return errModelInferenceTargetScope
	}
	actual, e := modelInferenceTargetDecode(raw)
	if e != nil || actual != connection.marker {
		return errModelInferenceTargetScope
	}
	var database, username string
	var relations int
	statementText := `SELECT DATABASE(),SUBSTRING_INDEX(CURRENT_USER(),'@',1),((SELECT count(*) FROM information_schema.tables WHERE table_schema=DATABASE())+(SELECT count(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE())+(SELECT count(*) FROM information_schema.routines WHERE routine_schema=DATABASE())+(SELECT count(*) FROM information_schema.events WHERE event_schema=DATABASE()))`
	if connection.store.QueryRow(ctx, transaction.SQL(connection.store.Dialect(), statementText)).Scan(&database, &username, &relations) != nil || database != actual.Database || username != actual.Username || relations != 0 {
		return errModelInferenceTargetScope
	}
	return nil
}

// Target catalogue commands explicitly enable this guard before any fixture
// migration. This test issues metadata reads only and never adopts existing data.
func TestModelInferenceTargetEmptyScope(t *testing.T) {
	if os.Getenv("PROJECT_ONE_MODEL_INFERENCE_TARGET_GUARD") != "1" {
		t.Skip("explicit isolated native target guard required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	connection, e := modelInferenceTargetOpen(ctx, true)
	if e != nil {
		t.Fatal(errModelInferenceTargetScope)
	}
	defer connection.close()
	if modelInferenceTargetVerify(ctx, connection, os.Getenv("PROJECT_ONE_MODEL_INFERENCE_TARGET_MARKER")) != nil {
		t.Fatal(errModelInferenceTargetScope)
	}
	t.Log("actual_read_only_private_empty_scope_guard=true migration_started=false")
}

func TestActualMySQLModelInferenceSchemaTargetScopeGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	connection, e := modelInferenceTargetOpen(ctx, false)
	if e != nil {
		t.Fatal(errModelInferenceTargetScope)
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
		if modelInferenceTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("actual empty scoped marker refused")
		}
	})
	t.Run("invalid_marker_or_path_refused", func(t *testing.T) {
		bad := connection.marker
		bad.Disposable = false
		notDisposable, _ := json.Marshal(bad)
		bad = connection.marker
		bad.Database = "m20_migration_00000000000000000000_test"
		if bad.Database == connection.marker.Database {
			bad.Database = "m20_migration_11111111111111111111_test"
		}
		foreign, _ := json.Marshal(bad)
		bad = connection.marker
		bad.ConnectionSHA256 = strings.Repeat("0", 64)
		changed, _ := json.Marshal(bad)
		for _, body := range [][]byte{nil, []byte(`{"kind":"SYNTHETIC_PRIVATE_MARKER"}`), append(append([]byte{}, raw...), []byte(` {}`)...), []byte(strings.Replace(string(raw), `"kind":`, `"kind":"duplicate","kind":`, 1)), notDisposable, foreign, changed} {
			writeMarker(body)
			if e := modelInferenceTargetVerify(ctx, connection, marker); e != errModelInferenceTargetScope {
				t.Fatal("invalid actual marker accepted or unsanitized")
			}
		}
		writeMarker(raw)
		os.Chmod(marker, 0644)
		if modelInferenceTargetVerify(ctx, connection, marker) == nil {
			t.Fatal("public marker accepted")
		}
		os.Chmod(marker, 0600)
		link := filepath.Join(root, "alias.json")
		if os.Symlink(marker, link) != nil {
			t.Fatal("symlink fixture")
		}
		if modelInferenceTargetVerify(ctx, connection, link) == nil {
			t.Fatal("symlink marker accepted")
		}
		os.Remove(link)
		if os.Link(marker, link) != nil {
			t.Fatal("hardlink fixture")
		}
		if modelInferenceTargetVerify(ctx, connection, marker) == nil {
			t.Fatal("hardlinked marker accepted")
		}
		os.Remove(link)
		if modelInferenceTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("explicit marker repair failed")
		}
	})
	t.Run("existing_relation_refused_without_migration", func(t *testing.T) {
		if modelInferenceTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("nonempty test did not start empty")
		}
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "CREATE TABLE model_inference_target_guard_fixture(value integer)")); e != nil {
			t.Fatal("owned guard sentinel creation")
		}
		dropped := false
		t.Cleanup(func() {
			if !dropped {
				clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, e := connection.store.Exec(clean, transaction.SQL(connection.store.Dialect(), "DROP TABLE model_inference_target_guard_fixture")); e != nil {
					t.Error("owned guard sentinel cleanup")
				}
			}
		})
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "INSERT INTO model_inference_target_guard_fixture(value) VALUES(1)")); e != nil {
			t.Fatal("owned guard sentinel data")
		}
		if modelInferenceTargetVerify(ctx, connection, marker) != errModelInferenceTargetScope {
			t.Fatal("nonempty actual scope was adopted")
		}
		var count int
		if connection.store.QueryRow(ctx, transaction.SQL(connection.store.Dialect(), "SELECT count(*) FROM model_inference_target_guard_fixture")).Scan(&count) != nil || count != 1 {
			t.Fatal("refused guard changed sentinel")
		}
		if _, e := connection.store.Exec(ctx, transaction.SQL(connection.store.Dialect(), "DROP TABLE model_inference_target_guard_fixture")); e != nil {
			t.Fatal("explicit guard fixture repair")
		}
		dropped = true
		if modelInferenceTargetVerify(ctx, connection, marker) != nil {
			t.Fatal("explicit empty-scope recovery failed")
		}
	})
	t.Run("existing_non_table_object_refused", func(t *testing.T) {
		for _, object := range []struct{ create, drop string }{
			{"CREATE VIEW model_inference_target_guard_view AS SELECT 1 AS value", "DROP VIEW model_inference_target_guard_view"},
			{"CREATE PROCEDURE model_inference_target_guard_procedure() SELECT 1", "DROP PROCEDURE model_inference_target_guard_procedure"},
		} {
			if modelInferenceTargetVerify(ctx, connection, marker) != nil {
				t.Fatal("non-table guard setup must be empty")
			}
			if _, e := connection.store.Exec(ctx, transaction.SQL(transaction.MySQL, object.create)); e != nil {
				t.Fatal("owned non-table sentinel creation", e)
			}
			dropped := false
			t.Cleanup(func() {
				if !dropped {
					clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if _, e := connection.store.Exec(clean, transaction.SQL(transaction.MySQL, object.drop)); e != nil {
						t.Error("owned non-table sentinel cleanup")
					}
				}
			})
			if modelInferenceTargetVerify(ctx, connection, marker) != errModelInferenceTargetScope {
				t.Fatal("existing non-table target object adopted")
			}
			if _, e := connection.store.Exec(ctx, transaction.SQL(transaction.MySQL, object.drop)); e != nil {
				t.Fatal("owned non-table sentinel repair", e)
			}
			dropped = true
			if modelInferenceTargetVerify(ctx, connection, marker) != nil {
				t.Fatal("non-table explicit recovery did not restore empty scope")
			}
		}
	})

}
