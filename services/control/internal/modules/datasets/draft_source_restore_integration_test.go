//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestActualParquetDraftAfterMetadataAndObjectRestore(t *testing.T) {
	if os.Getenv("OWNSOLID_TEST_DRAFT_RESTORE_CONTAINER") == "" {
		t.Skip("use testdata/replay_draft_import.sh to bind a disposable logical-restore cluster")
	}
	python := os.Getenv("OWNSOLID_TEST_PARQUET_PYTHON")
	if python == "" {
		t.Fatal("locked Parquet interpreter required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	account := fixture.New(t)
	owned := ownedDraftRestoreFixture(t, ctx, account.DB)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	actor := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	workspace, tenant := actor.Me.Workspace.ID, actor.Me.Workspace.TenantID
	processor, root := nativeProcessor(t)
	account.Config.Datasets = processor.configuration
	if err := InitializeStore(root, tenant, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(root, tenant, 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, Store: store, Processor: processor}
	parquet := &ParquetImportConfig{
		PythonExecutable: python, DecoderFile: filepath.Join(filepath.Dir(processor.configuration.ProcessorFile), "import_parquet.py"),
		BatchRows: 1, MetadataBytes: 1 << 20, RowGroupBytes: 8 << 20, MemoryBytes: 512 << 20,
	}
	other, err := service.Workspaces.Create(ctx, admin, "Other restore workspace", actor.Me.User.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	data, err := service.Create(ctx, actor, CreateRequest{
		WorkspaceID: workspace, Name: "Parquet restore fixture", Origin: "Synthetic fixture",
		License: "Synthetic CC0", RightsAsserted: true, Reason: "Exercise scoped restoration",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	// Populate the released draft path before applying any candidate SQL.
	legacyRaw := []byte("{\"source_id\":\"legacy-row\",\"prompt\":\"Old question\",\"completion\":\"Old answer\"}\n")
	legacySHA := fmt.Sprintf("%x", sha256.Sum256(legacyRaw))
	legacyKey := uuid.NewString()
	legacy, err := service.CreateDraft(ctx, actor, workspace, data.ID, DraftRequest{
		SourceName: "legacy.jsonl", SourceSHA256: legacySHA, ExpectedBytes: int64(len(legacyRaw)), Reason: "Preserve an existing draft",
	}, legacyKey, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Upload(ctx, actor, workspace, data.ID, legacy.ID, legacySHA, int64(len(legacyRaw)), bytes.NewReader(legacyRaw), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	process(t, service)
	legacy, err = service.GetDraft(ctx, actor, workspace, data.ID, legacy.ID)
	if err != nil || legacy.State != "editing" {
		t.Fatal("legacy draft was not inspected", err)
	}
	var predecessorSQL []byte
	if file := os.Getenv("OWNSOLID_TEST_DRAFT_RESTORE_PREDECESSOR_SQL"); file != "" {
		if !filepath.IsAbs(file) {
			t.Fatal("absolute owned SQL overlay path required")
		}
		predecessorSQL = readDraftMigration(t, file)
	}
	up := readDraftMigration(t, "testdata/draft_import_candidate.up.sql")
	down := readDraftMigration(t, "testdata/draft_import_candidate.down.sql")
	predecessor := owned.RestoreFresh(t, ctx, service.DB, owned.DumpSchema(t, ctx))
	for _, sql := range [][]byte{predecessorSQL, up, down} {
		applyDraftMigration(t, ctx, predecessor, sql)
	}
	var tableRemoved, keyRemoved bool
	if err = predecessor.QueryRow(ctx, `SELECT to_regclass('dataset_draft_import_candidates') IS NULL,
		NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='dataset_drafts'::regclass
		           AND conname='dataset_draft_import_scope_key')`).Scan(&tableRemoved, &keyRemoved); err != nil || !tableRemoved || !keyRemoved {
		t.Fatal("candidate rollback left catalog objects", err)
	}
	applyDraftMigration(t, ctx, predecessor, up)
	verifyLegacyDraftMigration(t, ctx, predecessor, legacy)
	applyDraftMigration(t, ctx, service.DB, predecessorSQL)
	applyDraftMigration(t, ctx, service.DB, up)
	verifyLegacyDraftMigration(t, ctx, service.DB, legacy)
	candidate, err := NewDraftSourceCandidate(service, parquet)
	if err != nil {
		t.Fatal(err)
	}

	script, err := filepath.Abs("testdata/draft_source_fixture.py")
	if err != nil {
		t.Fatal(err)
	}
	parquetBytes, err := exec.CommandContext(ctx, python, "-I", "-B", script, "valid").Output()
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(parquetBytes))
	input := DraftSourceRequest{Name: "original.parquet", SHA256: digest, Bytes: int64(len(parquetBytes))}
	intent := uuid.NewString()
	uploaded, err := candidate.Import(ctx, actor, workspace, data.ID, SourceParquet, input, intent, bytes.NewReader(parquetBytes), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	process(t, service)
	before, err := candidate.Read(ctx, actor, workspace, data.ID, uploaded.Draft.ID)
	if err != nil || before.Draft.State != "editing" || before.Report.SourceSHA256 != digest || before.Report.OutputSHA256 != before.Draft.SourceSHA256 {
		t.Fatal("source report was not readable before backup", err)
	}
	if _, err = service.DB.Exec(ctx, `INSERT INTO sites(id,display_name) VALUES('draft-alt-site','Other draft site');
		INSERT INTO cells(id,site_id,display_name) VALUES('draft-alt-cell',local_site_id(),'Other draft cell');`); err != nil {
		t.Fatal("seed registered foreign scopes", err)
	}
	verifyDraftScopeConstraint(t, ctx, service.DB, before.Draft.ID)
	var object string
	if err = service.DB.QueryRow(ctx, "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1 AND workspace_id=$2",
		before.Draft.ID, workspace).Scan(&object); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "objects", object, "source.parquet")
	backedUp, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(backedUp, parquetBytes) {
		t.Fatal("retained source differed before backup", err)
	}
	backup := filepath.Join(privateSnapshotDirectory(t), "files")
	if err = service.ExportFiles(ctx, backup); err != nil {
		t.Fatal("export existing normalized file inventory", err)
	}
	var snapshot FileSnapshot
	manifest, err := os.ReadFile(filepath.Join(backup, snapshotName))
	if err != nil || json.Unmarshal(manifest, &snapshot) != nil || snapshot.References.TenantID != tenant {
		t.Fatal("private snapshot inventory", err)
	}
	found := false
	for _, file := range snapshot.Files {
		if file.Path == objectPath(object) {
			found = file.SHA256 == before.Report.OutputSHA256 && file.SizeBytes == before.Report.OutputBytes
		}
		if strings.HasSuffix(file.Path, "/source.parquet") {
			t.Fatal("legacy snapshot silently admitted an original outside its inventory")
		}
	}
	if !found {
		t.Fatal("private normalized inventory lost its accepted digest")
	}
	archive := owned.DumpSchema(t, ctx)
	restored := owned.RestoreFresh(t, ctx, service.DB, archive)
	if restored.Config().ConnConfig.Database == service.DB.Config().ConnConfig.Database {
		t.Fatal("restore reused the live metadata database")
	}

	restoredRoot := privateSnapshotDirectory(t)
	restoredAccount := &identity.Service{
		DB: restored, Config: account.Config, Passwords: account.Passwords, HMACKey: account.HMACKey,
	}
	restoredAccount.Config.Datasets.ArtifactRoot = restoredRoot
	restoredAccount.BusinessAdmission, err = licensing.FromConfig(restoredAccount)
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Service{
		DB: restored, Identity: restoredAccount,
		Workspaces: &workspaces.Service{DB: restored, Identity: restoredAccount},
	}
	if err = pgx.BeginFunc(ctx, restored, func(tx pgx.Tx) error {
		return restoredAccount.InvalidateAccountAuthorities(ctx, tx, "Synthetic Parquet recovery")
	}); err != nil {
		t.Fatal("invalidate sessions in the restored account database", err)
	}
	if err = fresh.ImportFiles(ctx, backup, restoredRoot); err != nil {
		t.Fatal("restore normalized snapshot into the new store", err)
	}
	if err = fresh.OpenConfiguredStore(ctx); err != nil {
		t.Fatal("open restored dataset store", err)
	}
	t.Cleanup(func() { _ = fresh.Store.Close() })
	if fresh.Store.Namespace() != store.Namespace() {
		t.Fatal("restore changed the dataset store namespace")
	}
	reader, err := NewDraftSourceCandidate(fresh, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectFailure := func(a identity.Auth, w, expected string) {
		t.Helper()
		out, err := reader.Read(ctx, a, w, data.ID, before.Draft.ID)
		if err == nil || !strings.HasPrefix(err.Error(), expected) || !reflect.DeepEqual(out, DraftSourceResult{}) {
			t.Fatalf("expected a closed report read with %s, received %v", expected, err)
		}
	}
	expectFailure(actor, workspace, "AUTH_")
	reauth := fixture.Login(t, restoredAccount, "admin@example.invalid", password, "portal")
	if reauth.Me.User.ID != actor.Me.User.ID {
		t.Fatal("restored login did not recover the original subject")
	}
	verifyLegacyDraftMigration(t, ctx, restored, legacy)
	verifyDraftScopeConstraint(t, ctx, restored, before.Draft.ID)
	if got, err := fresh.GetDraft(ctx, reauth, workspace, data.ID, legacy.ID); err != nil || !reflect.DeepEqual(got, legacy) {
		t.Fatal("restored authorized legacy draft changed", err)
	}
	var legacyObject string
	if err = restored.QueryRow(ctx, "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1 AND workspace_id=$2",
		legacy.ID, workspace).Scan(&legacyObject); err != nil {
		t.Fatal("read restored legacy source reference", err)
	}
	legacySource := filepath.Join(restoredRoot, filepath.FromSlash(objectPath(legacyObject)))
	if raw, err := os.ReadFile(legacySource); err != nil || !bytes.Equal(raw, legacyRaw) {
		t.Fatal("restored legacy source changed", err)
	}
	expectFailure(reauth, other.ID, "DATASET_NOT_FOUND")
	expectFailure(reauth, workspace, "DATASET_FILE_INVALID")

	// The legacy snapshot restores only the normalized role. Replay the
	// supplemental original from the existing signed S3 test transport.
	const bucket, etag = "draft-restore-fixture", `"retained-parquet"`
	ref := objectstore.Ref{Tenant: tenant, Workspace: workspace, Object: object}
	key, err := ref.Key()
	if err != nil {
		t.Fatal(err)
	}
	client := newSourceFixtureS3(t, bucket, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/"+bucket+"/"+key ||
			r.Header.Get("If-Match") != etag || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("restore did not request the exact signed retained object")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(backedUp)))
		w.Header().Set("ETag", etag)
		_, _ = w.Write(backedUp)
	})
	body, err := openSourceObject(ctx, client, SourceObject{Ref: ref, ETag: etag, Bytes: before.Report.SourceBytes, SHA256: digest})
	if err != nil {
		t.Fatal("read the retained object through S3", err)
	}
	retained, err := io.ReadAll(io.LimitReader(body, before.Report.SourceBytes+1))
	closeErr := body.Close()
	if err != nil || closeErr != nil || int64(len(retained)) != before.Report.SourceBytes ||
		fmt.Sprintf("%x", sha256.Sum256(retained)) != before.Report.SourceSHA256 {
		t.Fatal("retained object does not match its durable receipt", err)
	}
	original = filepath.Join(restoredRoot, "objects", object, "source.parquet")
	if err = writeNew(original, retained); err != nil {
		t.Fatal(err)
	}
	got, err := reader.Read(ctx, reauth, workspace, data.ID, before.Draft.ID)
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("restored read changed the authorized draft or report", err)
	}
	expectFailure(reauth, other.ID, "DATASET_NOT_FOUND")

	normalized := filepath.Join(restoredRoot, filepath.FromSlash(objectPath(object)))
	normalizedSHA, normalizedSize, err := contextFileDigest(ctx, normalized, got.Report.OutputBytes)
	if err != nil || normalizedSHA != got.Report.OutputSHA256 || normalizedSize != got.Report.OutputBytes {
		t.Fatal("restored normalized file is inconsistent with the report", err)
	}
	for _, file := range []string{original, normalized} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		changed := bytes.Clone(raw)
		changed[0] ^= 1
		if err = os.WriteFile(file, changed, 0600); err != nil {
			t.Fatal(err)
		}
		expectFailure(reauth, workspace, "DATASET_FILE_INVALID")
		if err = os.Remove(file); err != nil {
			t.Fatal(err)
		}
		expectFailure(reauth, workspace, "DATASET_FILE_INVALID")
		if err = writeNew(file, raw); err != nil {
			t.Fatal(err)
		}
	}
	// The database admits a matching foreign parent/receipt pair. The local
	// service must still refuse it even when the Workspace and dataset match.
	if err = pgx.BeginFunc(ctx, restored, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "UPDATE dataset_drafts SET cell_id='draft-alt-cell' WHERE id=$1", legacy.ID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO dataset_draft_import_candidates
			(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report)
			SELECT d.tenant_id,d.workspace_id,d.dataset_id,d.id,d.site_id,d.cell_id,c.source_format,c.report
			FROM dataset_drafts d CROSS JOIN dataset_draft_import_candidates c
			WHERE d.id=$1 AND c.draft_id=$2`, legacy.ID, before.Draft.ID)
		return e
	}); err != nil {
		t.Fatal("construct a matching foreign draft receipt", err)
	}
	out, err := reader.Read(ctx, reauth, workspace, data.ID, legacy.ID)
	if err == nil || err.Error() != "DATASET_NOT_FOUND" || !reflect.DeepEqual(out, DraftSourceResult{}) {
		t.Fatal("matching foreign receipt crossed the local cell boundary", err)
	}
	refuseBeforeRead := func(key string) {
		t.Helper()
		body := bytes.NewReader(legacyRaw)
		out, err := reader.Import(ctx, reauth, workspace, data.ID, SourceCSV,
			DraftSourceRequest{Name: "foreign.csv", SHA256: legacySHA, Bytes: int64(len(legacyRaw))},
			key, body, uuid.NewString())
		if err == nil || err.Error() != "DATASET_NOT_FOUND" || !reflect.DeepEqual(out, DraftSourceResult{}) || body.Len() != len(legacyRaw) {
			t.Fatal("foreign dataset or draft admitted or consumed the import body", err)
		}
	}
	refuseBeforeRead(legacyKey)
	if _, err = restored.Exec(ctx, `UPDATE dataset_draft_import_candidates
		SET report=jsonb_set(report,'{output_sha256}',to_jsonb(repeat('0',64))) WHERE draft_id=$1`, before.Draft.ID); err != nil {
		t.Fatal(err)
	}
	expectFailure(reauth, workspace, "DATASET_DATA_INVALID")
	if _, err = restored.Exec(ctx, "DELETE FROM dataset_draft_import_candidates WHERE draft_id=$1", before.Draft.ID); err != nil {
		t.Fatal(err)
	}
	expectFailure(reauth, workspace, "DATASET_NOT_FOUND")
	if _, err = restored.Exec(ctx, "UPDATE datasets SET cell_id='draft-alt-cell' WHERE id=$1", data.ID); err != nil {
		t.Fatal(err)
	}
	refuseBeforeRead(uuid.NewString())
	t.Log("predecessor upgrade, rollback, restored scope, private normalized inventory, retained S3 original and digest refusals verified")
}

func readDraftMigration(t *testing.T, name string) []byte {
	t.Helper()
	sql, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return sql
}

func applyDraftMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql []byte) {
	t.Helper()
	if len(sql) == 0 {
		return
	}
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, string(sql))
		return err
	}); err != nil {
		t.Fatal("apply owned candidate or predecessor SQL", err)
	}
}

func verifyLegacyDraftMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, legacy Draft) {
	t.Helper()
	var state, sha string
	var size int64
	var local, report bool
	if err := pool.QueryRow(ctx, `SELECT d.state,d.source_sha256,d.expected_bytes,
		d.site_id=local_site_id() AND d.cell_id=local_cell_id(),
		EXISTS(SELECT 1 FROM dataset_draft_import_candidates c WHERE c.draft_id=d.id)
		FROM dataset_drafts d WHERE id=$1`, legacy.ID).Scan(&state, &sha, &size, &local, &report); err != nil ||
		state != legacy.State || sha != legacy.SourceSHA256 || size != legacy.ExpectedBytes || !local || report {
		t.Fatal("candidate DDL changed a legacy draft or manufactured a receipt", err)
	}
}

func verifyDraftScopeConstraint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, draft string) {
	t.Helper()
	var site, cell string
	var valid bool
	if err := pool.QueryRow(ctx, `SELECT d.site_id,d.cell_id,c.site_id=d.site_id AND c.cell_id=d.cell_id
		AND d.site_id=local_site_id() AND d.cell_id=local_cell_id()
		FROM dataset_drafts d JOIN dataset_draft_import_candidates c ON c.draft_id=d.id WHERE d.id=$1`, draft).
		Scan(&site, &cell, &valid); err != nil || !valid {
		t.Fatal("receipt did not inherit the local draft scope", err)
	}
	for _, tc := range []struct {
		site, cell any
		code       string
	}{
		{site, "draft-alt-cell", "23503"},
		{"draft-alt-site", cell, "23503"},
		{site, nil, "23502"},
	} {
		_, err := pool.Exec(ctx, "UPDATE dataset_draft_import_candidates SET site_id=$1,cell_id=$2 WHERE draft_id=$3", tc.site, tc.cell, draft)
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != tc.code || tc.code == "23503" && pg.ConstraintName != "dataset_draft_import_candidate_scope_fk" {
			t.Fatal("the receipt accepted a foreign or null scope", err)
		}
	}
}

func ownedDraftRestoreFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) fixture.PostgresFixture {
	t.Helper()
	container := os.Getenv("OWNSOLID_TEST_DRAFT_RESTORE_CONTAINER")
	dockerContext := os.Getenv("OWNSOLID_TEST_DRAFT_RESTORE_DOCKER_CONTEXT")
	expected := os.Getenv("OWNSOLID_TEST_DRAFT_RESTORE_CLUSTER")
	conf := pool.Config().ConnConfig
	if dockerContext == "" || !strings.HasPrefix(expected, "draft-import-") ||
		conf.Host != "127.0.0.1" || conf.Port != 5432 || conf.User != "postgres" || conf.Database != "ownsolid_draft_import_test" {
		t.Fatal("restore acceptance requires the exact disposable draft fixture")
	}
	info, err := exec.CommandContext(ctx, "docker", "--context", dockerContext, "inspect", "--type", "container", container,
		"--format", `{{.State.Running}} {{.HostConfig.NetworkMode}} {{index .Config.Labels "ownsolid.fixture.cluster"}}`).Output()
	if err != nil || strings.TrimSpace(string(info)) != "true none "+expected {
		t.Fatal("restore fixture is not a live network-none container", err)
	}
	var cluster, version string
	if err = pool.QueryRow(ctx, "SELECT current_setting('cluster_name'),current_setting('server_version_num')").Scan(&cluster, &version); err != nil ||
		cluster != expected || version != "180006" {
		t.Fatal("restore database does not match the declared isolated PostgreSQL 18.6 cluster", err)
	}
	return fixture.PostgresFixture{
		Container: container, Context: dockerContext, Database: conf.Database, User: conf.User, Schema: conf.RuntimeParams["search_path"],
	}
}
