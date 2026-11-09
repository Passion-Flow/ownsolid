//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	schema, err := os.ReadFile("testdata/draft_import_candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DB.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	parquet := &ParquetImportConfig{
		PythonExecutable: python, DecoderFile: filepath.Join(filepath.Dir(processor.configuration.ProcessorFile), "import_parquet.py"),
		BatchRows: 1, MetadataBytes: 1 << 20, RowGroupBytes: 8 << 20, MemoryBytes: 512 << 20,
	}
	candidate, err := NewDraftSourceCandidate(service, parquet)
	if err != nil {
		t.Fatal(err)
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
	if _, err = restored.Exec(ctx, `UPDATE dataset_draft_import_candidates
		SET report=jsonb_set(report,'{output_sha256}',to_jsonb(repeat('0',64))) WHERE draft_id=$1`, before.Draft.ID); err != nil {
		t.Fatal(err)
	}
	expectFailure(reauth, workspace, "DATASET_DATA_INVALID")
	if _, err = restored.Exec(ctx, "DELETE FROM dataset_draft_import_candidates WHERE draft_id=$1", before.Draft.ID); err != nil {
		t.Fatal(err)
	}
	expectFailure(reauth, workspace, "DATASET_NOT_FOUND")
	t.Log("logical archive, normalized file snapshot, retained S3 original, fresh authorization and digest refusals verified")
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
