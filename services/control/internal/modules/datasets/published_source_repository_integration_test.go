//go:build repository_integration

package datasets_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type publishedSourceJSON string
type publishedSourceStory struct {
	f                                       repositoryfixture.Fixture
	ctx                                     context.Context
	owner                                   *identity.Service
	s                                       *datasets.Service
	a                                       identity.Auth
	dataset, version, attempt, root, digest string
	manifest                                wire.Manifest
	payload                                 []byte
}

func (g publishedSourceStory) statement(pg, my string, args ...any) transaction.Statement {
	return g.f.Statement(pg, my, args...)
}
func (g publishedSourceStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.statement(pg, my, args...)); e != nil {
		t.Fatal("actual synthetic publication fixture", e)
	}
}

// SQL publication metadata is explicitly synthetic and all real constraints stay
// enabled. Only effective.jsonl is a real file fixture, not processor output.
func (g publishedSourceStory) seed(t *testing.T, table string, values map[string]any) {
	t.Helper()
	values["tenant_id"], values["workspace_id"] = g.f.Tenant, g.f.Workspace
	columns := make([]string, 0, len(values))
	for key := range values {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	var args []any
	var marks []string
	for _, key := range columns {
		mark := "?"
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			mark = fmt.Sprintf("$%d", len(args)+1)
		}
		v := values[key]
		if j, ok := v.(publishedSourceJSON); ok {
			v = string(j)
			if g.f.Store.Dialect() == transaction.PostgreSQL {
				mark += "::jsonb"
			} else {
				mark = "CAST(" + mark + " AS JSON)"
			}
		}
		args = append(args, v)
		marks = append(marks, mark)
	}
	query := "INSERT INTO " + table + "(" + strings.Join(columns, ",") + ") VALUES(" + strings.Join(marks, ",") + ")"
	if _, e := g.f.Store.Exec(g.ctx, transaction.SQL(g.f.Store.Dialect(), query, args...)); e != nil {
		t.Fatal("synthetic constrained published source seed", table, e)
	}
}
func publishedSourceSetup(t *testing.T) publishedSourceStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store}
	if f.PostgreSQL != nil {
		owner.Storage, owner.DB = nil, f.PostgreSQL
	}
	ws := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	root := filepath.Join(t.TempDir(), "published-source")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := datasets.InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := store.Close(); e != nil {
			t.Error(e)
		}
	})
	payload := []byte("{\"source_id\":\"synthetic-\u4e16\u754c\",\"prompt\":\"one\",\"completion\":\"two\"}\n")
	hash := sha256.Sum256(payload)
	digest := hex.EncodeToString(hash[:])
	g := publishedSourceStory{f: f, ctx: ctx, owner: owner, s: &datasets.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: ws, Store: store}, dataset: uuid.NewString(), version: uuid.NewString(), attempt: uuid.NewString(), root: root, digest: digest, payload: payload}
	var issued identity.IssuedSession
	if e = f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = owner.IssueSessionWithin(ctx, q, f.User, 1, "admin", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	g.a, e = owner.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	draft, job := uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 21, 1, 2, 3, 123456000, time.UTC)
	fp := bytes.Repeat([]byte{8}, 32)
	g.seed(t, "datasets", map[string]any{"id": g.dataset, "name": "Synthetic published \u4e16\u754c \u0627\u0644\u0639\u0631\u0628\u064a\u0629", "name_key": g.dataset, "origin": "synthetic", "license": "synthetic", "rights_asserted": true, "created_by": f.User, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_drafts", map[string]any{"id": draft, "dataset_id": g.dataset, "created_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "source_object_id": uuid.NewString(), "source_name": "synthetic.jsonl", "source_sha256": digest, "expected_bytes": len(payload), "source_received_at": now, "state": "published", "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_jobs", map[string]any{"id": job, "dataset_id": g.dataset, "draft_id": draft, "kind": "publish", "requested_by": f.User, "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic immutable publication source", "expected_revision": 1, "state": "succeeded", "version_id": g.version, "input_policy": publishedSourceJSON(`{}`), "split_seed": strings.Repeat("1", 32), "validation_basis_points": 1000, "completed_at": now, "creation_key": uuid.NewString(), "creation_fingerprint": fp})
	g.seed(t, "dataset_job_attempts", map[string]any{"id": g.attempt, "dataset_id": g.dataset, "draft_id": draft, "job_id": job, "attempt_number": 1, "state": "succeeded", "finished_at": now})
	manifest := wire.Manifest{SchemaVersion: 1, RowSchema: wire.RowSchema, ProcessorVersion: wire.ProcessorVersion, SplitAlgorithm: wire.SplitAlgorithm, SourceSHA256: digest, Seed: strings.Repeat("1", 32), ValidationBasisPoints: 1000, TotalRows: 2, UniqueRows: 2, SourceGroups: 2, ValidationGroups: 1, TrainRows: 1, ValidationRows: 1, ManifestFile: wire.Asset{Path: "manifest.json", SizeBytes: 2, SHA256: digest}}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl"} {
		manifest.Files = append(manifest.Files, wire.Asset{Path: name, SizeBytes: 0, SHA256: digest})
	}
	g.manifest = manifest
	raw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	g.seed(t, "dataset_versions", map[string]any{"id": g.version, "dataset_id": g.dataset, "draft_id": draft, "job_id": job, "attempt_id": g.attempt, "ordinal": 1, "published_by": f.User, "published_at": now, "raw_source_sha256": digest, "effective_source_sha256": digest, "manifest_sha256": digest, "manifest": publishedSourceJSON(raw), "row_mapping_sha256": digest})
	return g
}
func (g publishedSourceStory) within(fn func(context.Context, transaction.Executor) error) error {
	return g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := g.owner.RecheckOrderedWithin(ctx, q, g.a); e != nil {
			return e
		}
		if _, e := g.s.Workspaces.RecheckScopeWithin(ctx, q, g.a, g.f.Workspace); e != nil {
			return e
		}
		return fn(ctx, q)
	})
}
func (g publishedSourceStory) read(ctx context.Context, q transaction.Executor, retained bool) (datasets.Version, error) {
	return g.s.PublishedSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, retained)
}
func publishedSourceWant(t *testing.T, value datasets.Version, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code || !reflect.DeepEqual(value, datasets.Version{}) {
		t.Fatalf("expected zero version/%s, got error %v, zero=%v", code, e, reflect.DeepEqual(value, datasets.Version{}))
	}
}

type publishedSourceWrongDialect struct{ transaction.Executor }

func (q publishedSourceWrongDialect) Dialect() transaction.Dialect {
	if q.Executor.Dialect() == transaction.PostgreSQL {
		return transaction.MySQL
	}
	return transaction.PostgreSQL
}

func TestActualBorrowedPublishedDatasetSource(t *testing.T) {
	t.Run("typed_source_and_original_PG_wrapper", func(t *testing.T) {
		g := publishedSourceSetup(t)
		var got datasets.Version
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var e error
			got, e = g.read(ctx, q, false)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		if got.ID != g.version || got.DatasetID != g.dataset || got.WorkspaceID != g.f.Workspace || got.AttemptID != g.attempt || got.Ordinal != 1 || got.ParentVersionID != nil || got.RetiredAt != nil || got.RetireReason != nil || got.ManifestSHA256 != g.digest || !reflect.DeepEqual(got.Manifest, g.manifest) || got.PublishedAt.Location() != time.UTC {
			t.Fatal("typed immutable published source changed")
		}
		if g.f.PostgreSQL != nil {
			tx, e := g.f.PostgreSQL.Begin(g.ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(g.ctx)
			q := transaction.PostgreSQLTransaction(tx)
			if e = g.owner.RecheckOrderedWithin(g.ctx, q, g.a); e != nil {
				t.Fatal(e)
			}
			if _, e = g.s.Workspaces.RecheckScopeWithin(g.ctx, q, g.a, g.f.Workspace); e != nil {
				t.Fatal(e)
			}
			original, e := g.s.PublishedSourceInTransaction(g.ctx, tx, g.f.Tenant, g.f.Workspace, g.dataset, g.version, false)
			if e != nil || !reflect.DeepEqual(original, got) {
				t.Fatal("original PG wrapper differs", e)
			}
			if e = tx.Commit(g.ctx); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("exact_parent_version_and_scope_isolation", func(t *testing.T) {
		g := publishedSourceSetup(t)
		for _, ids := range [][4]string{{uuid.NewString(), g.f.Workspace, g.dataset, g.version}, {g.f.Tenant, uuid.NewString(), g.dataset, g.version}, {g.f.Tenant, g.f.Workspace, uuid.NewString(), g.version}, {g.f.Tenant, g.f.Workspace, g.dataset, uuid.NewString()}, {g.f.Tenant, g.f.Workspace, "bad", g.version}} {
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				v, e := g.s.PublishedSourceWithin(ctx, q, ids[0], ids[1], ids[2], ids[3], true)
				publishedSourceWant(t, v, e, "DATASET_NOT_FOUND")
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("retirement_and_retained_review", func(t *testing.T) {
		g := publishedSourceSetup(t)
		rollback := errors.New("EXPLICIT_TEST_RETIREMENT_ROLLBACK")
		for _, table := range []string{"datasets", "dataset_versions"} {
			id := g.dataset
			if table == "dataset_versions" {
				id = g.version
			}
			e := g.within(func(ctx context.Context, q transaction.Executor) error {
				if _, e := q.Exec(ctx, g.statement("UPDATE "+table+" SET retired_at=clock_timestamp(),retire_reason='Synthetic retirement' WHERE id=$1", "UPDATE "+table+" SET retired_at=UTC_TIMESTAMP(6),retire_reason='Synthetic retirement' WHERE id=?", id)); e != nil {
					return e
				}
				v, e := g.read(ctx, q, false)
				publishedSourceWant(t, v, e, "DATASET_RETIRED")
				v, e = g.read(ctx, q, true)
				if e != nil || v.ID != g.version {
					t.Fatal("retained source unavailable", e)
				}
				if table == "dataset_versions" && (v.RetiredAt == nil || v.RetiredAt.Location() != time.UTC) {
					t.Fatal("retirement timestamp lost")
				}
				return rollback
			})
			if e != rollback {
				t.Fatal("retirement owner outcome", e)
			}
		}
	})
	t.Run("manifest_source_and_digest_corruption_zero_projection", func(t *testing.T) {
		g := publishedSourceSetup(t)
		rollback := errors.New("EXPLICIT_TEST_CORRUPTION_ROLLBACK")
		variants := []map[string]any{{"manifest": map[string]any{}}, {"effective_source_sha256": strings.Repeat("2", 64)}, {"manifest_sha256": strings.Repeat("2", 64)}}
		for _, v := range variants {
			e := g.within(func(ctx context.Context, q transaction.Executor) error {
				for column, value := range v {
					pg, my := "UPDATE dataset_versions SET "+column+"=$1 WHERE id=$2", "UPDATE dataset_versions SET "+column+"=? WHERE id=?"
					if column == "manifest" {
						raw, _ := json.Marshal(value)
						value = string(raw)
						pg = "UPDATE dataset_versions SET manifest=$1::jsonb WHERE id=$2"
						my = "UPDATE dataset_versions SET manifest=CAST(? AS JSON) WHERE id=?"
					}
					if _, e := q.Exec(ctx, g.statement(pg, my, value, g.version)); e != nil {
						return e
					}
				}
				value, e := g.read(ctx, q, true)
				publishedSourceWant(t, value, e, "DATASET_DATA_INVALID")
				return rollback
			})
			if e != rollback {
				t.Fatal("corruption fixture rollback failed", e)
			}
		}
	})
	t.Run("actual_effective_file_failure", func(t *testing.T) {
		g := publishedSourceSetup(t)
		var value datasets.Version
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var e error
			value, e = g.read(ctx, q, true)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		assertFailure := func() {
			file, size, e := g.s.OpenPublishedSource(g.ctx, value, 4096)
			if e == nil || e.Error() != "DATASET_FILE_INVALID" || file != nil || size != 0 {
				if file != nil {
					file.Close()
				}
				t.Fatal("missing/changed effective source escaped", e)
			}
		}
		assertFailure()
		directory := filepath.Join(g.root, "versions", g.version, g.attempt)
		if e := os.MkdirAll(directory, 0700); e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(directory, "effective.jsonl")
		if e := os.WriteFile(path, g.payload, 0600); e != nil {
			t.Fatal(e)
		}
		file, size, e := g.s.OpenPublishedSource(g.ctx, value, 4096)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(file)
		closeErr := file.Close()
		if e != nil || closeErr != nil || size != int64(len(g.payload)) || !bytes.Equal(raw, g.payload) {
			t.Fatal("actual private effective source bytes changed", e)
		}
		changed := bytes.Clone(g.payload)
		changed[len(changed)-2] = 'x'
		if e = os.WriteFile(path, changed, 0600); e != nil {
			t.Fatal(e)
		}
		assertFailure()
	})
	t.Run("actual_parent_and_version_row_locks", func(t *testing.T) {
		g := publishedSourceSetup(t)
		for _, table := range []string{"datasets", "dataset_versions"} {
			if e := g.within(func(ctx context.Context, q transaction.Executor) error {
				if _, e := g.read(ctx, q, true); e != nil {
					return e
				}
				id := g.dataset
				if table == "dataset_versions" {
					id = g.version
				}
				blocked, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
				start := time.Now()
				_, e := g.f.Store.Exec(blocked, g.statement("UPDATE "+table+" SET retire_reason=retire_reason WHERE id=$1", "UPDATE "+table+" SET retire_reason=retire_reason WHERE id=?", id))
				if e == nil || time.Since(start) < 150*time.Millisecond {
					t.Fatal("borrowed source row fence missing", table, e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	})
	t.Run("owner_rollback_and_reopen", func(t *testing.T) {
		g := publishedSourceSetup(t)
		rollback := errors.New("EXPLICIT_TEST_SOURCE_OWNER_ROLLBACK")
		e := g.within(func(ctx context.Context, q transaction.Executor) error {
			if _, e := g.read(ctx, q, true); e != nil {
				return e
			}
			if _, e := q.Exec(ctx, g.statement("UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='Rolled back' WHERE id=$1", "UPDATE datasets SET retired_at=UTC_TIMESTAMP(6),retire_reason='Rolled back' WHERE id=?", g.dataset)); e != nil {
				return e
			}
			return rollback
		})
		if e != rollback {
			t.Fatal("owner rollback changed", e)
		}
		service := *g.s
		if e = g.within(func(ctx context.Context, q transaction.Executor) error {
			v, e := service.PublishedSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, false)
			if e != nil || v.ID != g.version {
				t.Fatal("new source transaction did not recover", e)
			}
			return e
		}); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("nil_mixed_context_and_query_failure", func(t *testing.T) {
		g := publishedSourceSetup(t)
		if e := g.within(func(ctx context.Context, q transaction.Executor) error {
			var absent *datasets.Service
			v, e := absent.PublishedSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, true)
			publishedSourceWant(t, v, e, "STORAGE_UNAVAILABLE")
			missing := *g.s
			missing.Store = nil
			v, e = missing.PublishedSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, true)
			publishedSourceWant(t, v, e, "DATASET_STORAGE_UNAVAILABLE")
			for _, test := range []struct {
				ctx  context.Context
				q    transaction.Executor
				code string
			}{{nil, q, "STORAGE_UNAVAILABLE"}, {ctx, nil, "STORAGE_UNAVAILABLE"}, {ctx, publishedSourceWrongDialect{q}, "DATASET_STORAGE_MISMATCH"}} {
				v, e := g.s.PublishedSourceWithin(test.ctx, test.q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, true)
				publishedSourceWant(t, v, e, test.code)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			v, e = g.read(cancelled, q, true)
			publishedSourceWant(t, v, e, "STORAGE_CANCELLED")
			expired, expire := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer expire()
			v, e = g.read(expired, q, true)
			publishedSourceWant(t, v, e, "STORAGE_TIMEOUT")
			incoherent := *g.s
			incoherent.Workspaces = &workspaces.Service{}
			v, e = incoherent.PublishedSourceWithin(ctx, q, g.f.Tenant, g.f.Workspace, g.dataset, g.version, true)
			publishedSourceWant(t, v, e, "DATASET_IDENTITY_REQUIRED")
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		g.exec(t, "ALTER TABLE dataset_versions RENAME TO source_test_retained_versions", "RENAME TABLE dataset_versions TO source_test_retained_versions")
		defer g.exec(t, "ALTER TABLE source_test_retained_versions RENAME TO dataset_versions", "RENAME TABLE source_test_retained_versions TO dataset_versions")
		e := g.within(func(ctx context.Context, q transaction.Executor) error {
			v, e := g.read(ctx, q, true)
			publishedSourceWant(t, v, e, "STORAGE_UNAVAILABLE")
			return e
		})
		if e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("real query failure lost", e)
		}
	})
}
