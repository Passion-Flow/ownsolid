//go:build repository_integration && object_storage_integration && object_storage_output_integration

package artifactstores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func objectOutputSetup(t *testing.T) *objectStory {
	t.Helper()
	g := objectSetup(t)
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("EXPLICIT_OUTPUT_CASE_DEADLINE_REQUIRED")
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	g.ctx = ctx
	return g
}
func objectArchive(t *testing.T, path string) *os.File {
	t.Helper()
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || actual != path {
		t.Fatal("EXACT_EXISTING_APPROVED_ARCHIVE_REQUIRED")
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		t.Fatal(e)
	}
	info, e := file.Stat()
	if e != nil {
		file.Close()
		t.Fatal(e)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || info.Mode().Perm()&0133 != 0 || info.Size() < modelartifact.MinimumArchiveBytes() {
		file.Close()
		t.Fatal("IMMUTABLE_OWNED_APPROVED_ARCHIVE_REQUIRED")
	}
	return file
}
func (g *objectStory) readyArchive(t *testing.T, file *os.File) Object {
	t.Helper()
	before, e := file.Stat()
	if e != nil {
		t.Fatal(e)
	}
	if before.Size() > g.s.Configuration.MaximumObjectBytes {
		t.Fatal("CONFIGURED_S3_MODEL_ARCHIVE_LIMIT_REQUIRED")
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, file); e != nil {
		t.Fatal(e)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	input := CreateRequest{WorkspaceID: g.f.Workspace, Name: "Actual approved model archive S3 ingress", Purpose: "model_bundle", ExpectedBytes: before.Size(), SHA256: digest, Reason: "Verify actual approved archive ingress"}
	o, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.work(t)
	for number, offset := int32(1), int64(0); offset < before.Size(); number, offset = number+1, offset+o.PartBytes {
		length := min(o.PartBytes, before.Size()-offset)
		hash := sha256.New()
		if _, e = io.Copy(hash, io.NewSectionReader(file, offset, length)); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.PutPart(g.ctx, g.admin, g.f.Workspace, o.ID, objectstore.Part{Number: number, Bytes: length, SHA256: hex.EncodeToString(hash.Sum(nil))}, io.NewSectionReader(file, offset, length), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	after, e := file.Stat()
	if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("APPROVED_ARCHIVE_CHANGED")
	}
	if _, e = g.s.Complete(g.ctx, g.admin, g.f.Workspace, o.ID, "Verify whole approved archive readback", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	g.work(t)
	o, e = g.s.Get(g.ctx, g.admin, g.f.Workspace, o.ID, "")
	if e != nil || o.State != "ready" {
		t.Fatal(e)
	}
	return o
}

type objectReceiptUnknownStore struct {
	transaction.Store
	tenant, workspace, object, job string
	fired                          bool
}

func (s *objectReceiptUnknownStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	unknown := false
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if ro || s.fired {
			return nil
		}
		var submitted bool
		if e := q.QueryRow(ctx, statement(q, "SELECT EXISTS(SELECT 1 FROM artifact_object_ingestions WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 AND state='submitted')", "SELECT EXISTS(SELECT 1 FROM artifact_object_ingestions WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? AND state='submitted')", `SELECT CASE WHEN EXISTS(SELECT 1 FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? AND "STATE"='submitted') THEN 1 ELSE 0 END FROM DUAL`, s.tenant, s.workspace, s.object, s.job)).Scan(&submitted); e != nil {
			return e
		}
		unknown = submitted
		return nil
	})
	if e == nil && unknown {
		s.fired = true
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func TestActualPrivateObjectStorageOutputs(t *testing.T) {
	for _, leaf := range []string{"approved_model_archive_real_publication", "dataset_real_processor_publication", "dataset_unknown_receipt_replay_preserves_edits"} {
		t.Run(leaf, func(t *testing.T) {
			g := objectOutputSetup(t)
			if leaf == "approved_model_archive_real_publication" {
				source := objectArchive(t, os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_ARCHIVE_FILE"))
				defer source.Close()
				o := g.readyArchive(t, source)
				input := ModelImportRequest{Name: "Actual S3 model publication", CatalogDigest: wire.ModelCatalogDigest(), LicenseAcknowledged: true, Reason: "Submit actual verified approved model"}
				key := uuid.NewString()
				job, e := g.s.IngestModel(g.ctx, g.admin, g.f.Workspace, o.ID, "", input, key, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				g.work(t)
				receipt, e := g.s.GetIngestion(g.ctx, g.admin, g.f.Workspace, o.ID, job.ID)
				if e != nil || receipt.State != "submitted" || receipt.TargetID == nil {
					t.Fatal("actual local import receipt", e)
				}
				worked, e := g.s.Models.ProcessNext(g.ctx)
				if e != nil || !worked {
					t.Fatal("actual model owner worker", e)
				}
				target, e := g.s.Models.GetImport(g.ctx, g.admin, *receipt.TargetID)
				if e != nil || target.State != "ready" || target.ModelID == nil {
					t.Fatal("actual model publication", e)
				}
				if e = modelartifact.VerifyDirectory(g.ctx, filepath.Join(g.s.Models.RootDirectory, "objects", *target.ModelID)); e != nil {
					t.Fatal(e)
				}
				versions, e := g.s.Models.List(g.ctx, g.portal, g.f.Workspace)
				if e != nil || len(versions) != 1 || versions[0].ID != *target.ModelID || versions[0].Kind != "approved_base" || versions[0].CatalogDigest != wire.ModelCatalogDigest() {
					t.Fatal("actual scoped approved model inventory", e)
				}
				again, e := g.s.IngestModel(g.ctx, g.admin, g.f.Workspace, o.ID, "", input, key, uuid.NewString())
				if e != nil || again.ID != job.ID || again.TargetID == nil || *again.TargetID != *receipt.TargetID {
					t.Fatal("model receipt replay changed target", e)
				}
				g.removeSubmittedSource(t, g.admin, o, receipt)
				if e = modelartifact.VerifyDirectory(g.ctx, filepath.Join(g.s.Models.RootDirectory, "objects", *target.ModelID)); e != nil {
					t.Fatal("source deletion changed published model", e)
				}
				return
			}
			source := []byte("{\"source_id\":\"one\",\"prompt\":\"Synthetic \u4e16\u754c\",\"completion\":\"Answer one\"}\n{\"source_id\":\"two\",\"prompt\":\"Synthetic \u0627\u0644\u0639\u0631\u0628\u064a\u0629\",\"completion\":\"Answer two\"}\n{\"source_id\":\"three\",\"prompt\":\"Synthetic three\",\"completion\":\"Answer three\"}\n{\"source_id\":\"four\",\"prompt\":\"Synthetic four\",\"completion\":\"Answer four\"}\n")
			o := g.ready(t, source)
			dataset, e := g.s.Datasets.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Actual S3 JSONL publication", Origin: "Explicit synthetic fixture", License: "CC0 synthetic fixture", RightsAsserted: true, Reason: "Create real dataset owner target"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			input := DatasetImportRequest{DatasetID: dataset.ID, SourceName: "synthetic.jsonl", Reason: "Submit real S3 source to actual processor"}
			key := uuid.NewString()
			job, e := g.s.IngestDataset(g.ctx, g.portal, g.f.Workspace, o.ID, "", input, key, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if leaf == "dataset_unknown_receipt_replay_preserves_edits" {
				wrapped := &objectReceiptUnknownStore{Store: g.f.Store, tenant: g.f.Tenant, workspace: g.f.Workspace, object: o.ID, job: job.ID}
				g.owner.Storage = wrapped
				worked, e := g.s.ProcessNext(g.ctx)
				g.owner.Storage = g.f.Store
				if worked || e == nil || e.Error() != "STORAGE_COMMIT_OUTCOME_UNKNOWN" || !wrapped.fired {
					t.Fatal("actual receipt unknown control missing", e)
				}
			} else {
				g.work(t)
			}
			receipt, e := g.s.GetIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID)
			if e != nil || receipt.State != "submitted" || receipt.TargetID == nil {
				t.Fatal("actual dataset receipt", e)
			}
			worked, e := g.s.Datasets.ProcessNext(g.ctx)
			if e != nil || !worked {
				t.Fatal("actual installed dataset processor", e)
			}
			draft, e := g.s.Datasets.GetDraft(g.ctx, g.portal, g.f.Workspace, dataset.ID, *receipt.TargetID)
			if e != nil || draft.State != "editing" || draft.SourceReceivedAt == nil || draft.InvalidRows == nil || *draft.InvalidRows != 0 || draft.TotalRows == nil || *draft.TotalRows != 4 {
				t.Fatal("actual parsed multilingual JSONL", e)
			}
			if leaf == "dataset_unknown_receipt_replay_preserves_edits" {
				page, e := g.s.Datasets.RowPage(g.ctx, g.portal, g.f.Workspace, dataset.ID, draft.ID, url.Values{"limit": {"2"}})
				if e != nil || len(page.Items) < 1 {
					t.Fatal(e)
				}
				row := page.Items[0]
				changed, e := g.s.Datasets.ReplaceRow(g.ctx, g.portal, g.f.Workspace, dataset.ID, draft.ID, row.ID, datasets.RowRequest{ExpectedRevision: draft.Revision, RowRevision: row.Revision, SourceID: *row.SourceID, Prompt: "Keep actual operator edit", Completion: *row.Completion, Reason: "Verify accepted source replay preserves edits"}, uuid.NewString())
				if e != nil || changed.Prompt == nil || *changed.Prompt != "Keep actual operator edit" {
					t.Fatal(e)
				}
				again, e := g.s.IngestDataset(g.ctx, g.portal, g.f.Workspace, o.ID, "", input, key, uuid.NewString())
				if e != nil || again.ID != job.ID || again.TargetID == nil || *again.TargetID != draft.ID {
					t.Fatal("receipt replay reset target", e)
				}
				g.removeSubmittedSource(t, g.portal, o, receipt)
				page, e = g.s.Datasets.RowPage(g.ctx, g.portal, g.f.Workspace, dataset.ID, draft.ID, url.Values{"limit": {"2"}})
				if e != nil || len(page.Items) < 1 || page.Items[0].Prompt == nil || *page.Items[0].Prompt != "Keep actual operator edit" {
					t.Fatal("operator edit was overwritten", e)
				}
				if worked, e = g.s.ProcessNext(g.ctx); e != nil || worked {
					t.Fatal("receipt replay relaunched source copy", e)
				}
				return
			}
			publication, e := g.s.Datasets.Publish(g.ctx, g.portal, g.f.Workspace, dataset.ID, draft.ID, datasets.PublicationRequest{ExpectedRevision: draft.Revision, Seed: strings.Repeat("1", 32), ValidationBasisPoints: g.owner.Config.Policy().Datasets.ValidationBasisPoints, Reason: "Publish actual processor output from private S3"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			worked, e = g.s.Datasets.ProcessNext(g.ctx)
			if e != nil || !worked {
				t.Fatal(e)
			}
			publication, e = g.s.Datasets.GetJob(g.ctx, g.portal, g.f.Workspace, dataset.ID, publication.ID)
			if e != nil || publication.State != "succeeded" || publication.VersionID == nil {
				t.Fatal("actual dataset publication job", e)
			}
			version, e := g.s.Datasets.GetVersion(g.ctx, g.portal, g.f.Workspace, dataset.ID, *publication.VersionID)
			if e != nil || version.RawSourceSHA256 != o.SHA256 || version.Manifest.TotalRows != 4 || version.Manifest.TrainRows < 1 || version.Manifest.ValidationRows < 1 {
				t.Fatal("actual partition manifest", e)
			}
			g.removeSubmittedSource(t, g.portal, o, receipt)
			for _, asset := range []string{"samples", "train", "validation", "assignments", "duplicates", "manifest"} {
				file, meta, e := g.s.Datasets.VersionFile(g.ctx, g.portal, g.f.Workspace, dataset.ID, version.ID, asset)
				if e != nil {
					t.Fatal(e)
				}
				hash := sha256.New()
				n, e := io.Copy(hash, file)
				file.Close()
				if e != nil || n != meta.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != meta.SHA256 {
					t.Fatal("actual published asset digest", asset, e)
				}
			}
			if e = g.s.Datasets.VerifyStored(g.ctx); e != nil {
				t.Fatal(e)
			}
		})
	}
}
