//go:build m07_model_recovery_integration || m08_combined_recovery_integration

package recovery_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

type datasetRecoveryState struct{ root, relative, digest, version, manifest string }

func prepareDatasetRecovery(t *testing.T, s *identity.Service, a identity.Auth, dir string) datasetRecoveryState {
	t.Helper()
	ctx := t.Context()
	root := filepath.Join(dir, "source-datasets")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	repo, _ := filepath.Abs("../../../../../")
	s.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: os.Getenv("PROJECT_ONE_DATA_PYTHON"), ProcessorFile: filepath.Join(repo, "runtimes/data-processing/process.py")}
	if e := datasets.InitializeStore(root, a.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	service := &datasets.Service{DB: s.DB, Identity: s, Workspaces: &workspaces.Service{DB: s.DB, Identity: s}}
	if e := service.OpenConfiguredStore(ctx); e != nil {
		t.Fatal(e)
	}
	defer service.Store.Close()
	data, e := service.Create(ctx, a, datasets.CreateRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Combined recovery corpus", Origin: "Synthetic local JSONL", License: "Synthetic CC0", RightsAsserted: true, Reason: "Actual combined file/SQL recovery"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	raw := []byte("{\"source_id\":\"A\",\"prompt\":\"one \u4e2d\u6587\",\"completion\":\"first\"}\n{\"source_id\":\"B\",\"prompt\":\"two\",\"completion\":\"second\"}\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	makeDraft := func(name string, upload bool) datasets.Draft {
		t.Helper()
		draft, e := service.CreateDraft(ctx, a, a.Me.Workspace.ID, data.ID, datasets.DraftRequest{SourceName: name, ExpectedBytes: int64(len(raw)), SourceSHA256: digest, Reason: "Retained source"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if upload {
			draft, e = service.Upload(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, digest, int64(len(raw)), bytes.NewReader(raw), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
		}
		return draft
	}
	draft := makeDraft("published.jsonl", true)
	if worked, e := service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal(e)
	}
	draft, e = service.GetDraft(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	job, e := service.Publish(ctx, a, a.Me.Workspace.ID, data.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Immutable actual Python version"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal(e)
	}
	version, e := service.GetVersion(ctx, a, a.Me.Workspace.ID, data.ID, *job.VersionID)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit synthetic active-at-crash metadata exercises every restore fence;
	// this is not presented as an executing Python child or a successful attempt.
	pending := makeDraft("interrupted.jsonl", true)
	attempt := uuid.NewString()
	if _, e = s.DB.Exec(ctx, "UPDATE dataset_jobs SET state='running',attempt_count=1,attempt_id=$2,lease_expires_at=clock_timestamp()+interval '1 day' WHERE id=$1", *pending.CurrentJobID, attempt); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, "INSERT INTO dataset_job_attempts(tenant_id,workspace_id,dataset_id,draft_id,job_id,id,attempt_number,state) VALUES($1,$2,$3,$4,$5,$6,1,'running')", a.Me.Workspace.TenantID, a.Me.Workspace.ID, data.ID, pending.ID, *pending.CurrentJobID, attempt); e != nil {
		t.Fatal(e)
	}
	uploading := makeDraft("incomplete.jsonl", false)
	if _, e = s.DB.Exec(ctx, "UPDATE dataset_drafts SET upload_attempt_id=$2,upload_expires_at=clock_timestamp()+interval '1 day' WHERE id=$1", uploading.ID, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	var object string
	if e = s.DB.QueryRow(ctx, "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1", draft.ID).Scan(&object); e != nil {
		t.Fatal(e)
	}
	return datasetRecoveryState{root: root, relative: filepath.Join("objects", object, "source.jsonl"), digest: digest, version: version.ID, manifest: version.ManifestSHA256}
}
func verifyRestoredDataset(t *testing.T, p *pgxpool.Pool, state datasetRecoveryState) {
	t.Helper()
	var active int
	if e := p.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM dataset_jobs WHERE state IN ('pending','running'))+(SELECT count(*) FROM dataset_job_attempts WHERE state='running')+(SELECT count(*) FROM dataset_drafts WHERE upload_attempt_id IS NOT NULL OR upload_expires_at IS NOT NULL)").Scan(&active); e != nil || active != 0 {
		t.Fatal("dataset authority survived restore", active, e)
	}
	var digest string
	if e := p.QueryRow(t.Context(), "SELECT manifest_sha256 FROM dataset_versions WHERE id=$1", state.version).Scan(&digest); e != nil || digest != state.manifest {
		t.Fatal("immutable version changed", e)
	}
	raw, e := os.ReadFile(filepath.Join(state.root, state.relative))
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(raw)) != state.digest {
		t.Fatal("restored source bytes changed", e)
	}
}
