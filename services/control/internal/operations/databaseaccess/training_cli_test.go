//go:build m07_delivery_integration || m08_delivery_integration || m09_delivery_integration

package databaseaccess

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Actual M08 APIs and pinned processor create source-connected immutable files
// before the delivered M08 principal/configuration is upgraded. Only data is synthetic.
func seedPublishedUpgradeDataset(t *testing.T, f actualFixture, password string) map[string]os.FileInfo {
	t.Helper()
	ctx := t.Context()
	c := f.service.Config.Datasets
	if e := os.Mkdir(c.ArtifactRoot, 0700); e != nil {
		t.Fatal(e)
	}
	a := identityfixture.Login(t, f.service, "admin@example.invalid", password, "admin")
	tenant := a.Me.Workspace.TenantID
	if e := datasets.InitializeStore(c.ArtifactRoot, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(c.ArtifactRoot, tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	processor, e := datasets.NewProcessor(c, config.DefaultDatasetPolicy())
	if e != nil {
		t.Fatal(e)
	}
	s := &datasets.Service{DB: f.service.DB, Identity: f.service, Workspaces: &workspaces.Service{DB: f.service.DB, Identity: f.service}, Store: store, Processor: processor}
	d, e := s.Create(ctx, a, datasets.CreateRequest{WorkspaceID: a.Me.Workspace.ID, Name: "Retained M08 training source", Origin: "Synthetic native upgrade records", License: "Synthetic CC0", RightsAsserted: true, Reason: "Preserve M08 lineage across actual upgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	for i := 0; i < 10; i++ {
		b, _ := json.Marshal(wire.Record{SourceID: fmt.Sprintf("group-%d", i), Prompt: fmt.Sprintf("Synthetic upgrade prompt %d", i), Completion: "Retained completion \u4e16\u754c"})
		raw.Write(b)
		raw.WriteByte('\n')
	}
	sum := sha256.Sum256(raw.Bytes())
	draft, e := s.CreateDraft(ctx, a, a.Me.Workspace.ID, d.ID, datasets.DraftRequest{SourceName: "retained-upgrade.jsonl", ExpectedBytes: int64(raw.Len()), SourceSHA256: hex.EncodeToString(sum[:]), Reason: "Retained source upload"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Upload(ctx, a, a.Me.Workspace.ID, d.ID, draft.ID, draft.SourceSHA256, draft.ExpectedBytes, &raw, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if worked, e := s.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("source processing", e)
	}
	draft, e = s.GetDraft(ctx, a, a.Me.Workspace.ID, d.ID, draft.ID)
	if e != nil {
		t.Fatal(e)
	}
	job, e := s.Publish(ctx, a, a.Me.Workspace.ID, d.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Preserve immutable train/validation lineage"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := s.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("published processing", e)
	}
	job, e = s.GetJob(ctx, a, a.Me.Workspace.ID, d.ID, job.ID)
	if e != nil || job.State != "succeeded" || job.VersionID == nil {
		t.Fatal("published version", e)
	}
	result := map[string]os.FileInfo{}
	if e = filepath.Walk(c.ArtifactRoot, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			result[path] = info
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	t.Log("Retained actual M08 source/version/lineage/files", len(result))
	return result
}
