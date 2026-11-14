//go:build repository_integration

package datasets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// No MongoDB server or processor is involved: this gate begins with a synthetic
// durable export and exercises the real M08 transactions, files and audit rows.
type mongoAttachmentReader struct {
	io.Reader
	before func()
}

func (r *mongoAttachmentReader) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.Reader.Read(p)
}

func TestMongoDBAttachmentFenceWithinDraftAndUploadTransactions(t *testing.T) {
	for _, name := range []string{"current_admin", "current_portal", "ordinary_upload", "changed_before_draft", "changed_before_upload", "changed_during_upload", "lease_expired_during_upload", "attempt_replaced_during_upload", "membership_replaced_during_upload"} {
		t.Run(name, func(t *testing.T) {
			account := fixture.New(t)
			password := fixture.Bootstrap(t, account)
			audience := "portal"
			if name == "current_admin" {
				audience = "admin"
			}
			a := fixture.Login(t, account, "admin@example.invalid", password, audience)
			ctx := t.Context()
			workspace := a.Me.Workspace.ID
			root := filepath.Join(t.TempDir(), "datasets")
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
			if e := InitializeStore(root, a.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
				t.Fatal(e)
			}
			store, e := OpenStore(root, a.Me.Workspace.TenantID, 4096)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if e := store.Close(); e != nil {
					t.Error(e)
				}
			})
			s := &Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}, Store: store, Processor: &Processor{}}
			data, e := s.Create(ctx, a, CreateRequest{WorkspaceID: workspace, Name: "Attachment " + uuid.NewString(), Origin: "Synthetic exported snapshot", License: "Synthetic CC0", RightsAsserted: true, Reason: "Exercise connector transaction fence"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			uri := filepath.Join(t.TempDir(), "uri")
			if e = os.Chmod(filepath.Dir(uri), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(uri, []byte("mongodb://fixture:synthetic-first@127.0.0.1/training?authSource=admin"), 0600); e != nil {
				t.Fatal(e)
			}
			source := config.MongoDBSource{ID: "attachment", TenantID: a.Me.Workspace.TenantID, WorkspaceID: workspace, URIFile: uri, Database: "training", Collection: "m21_examples", Fields: []string{"question", "answer"}}
			p := config.DefaultMongoDBConfiguration()
			p.Enabled = true
			p.Sources = []config.MongoDBSource{source}
			account.Config.MongoDB = p
			captured, e := mongodatasets.CaptureSource(source, p)
			if e != nil {
				t.Fatal(e)
			}
			input := MongoDBRequest{SourceID: source.ID, PromptField: "question", CompletionField: "answer", Conversion: "string", Reason: "Use synthetic durable export"}
			receipt, e := s.mongoReserve(ctx, a, workspace, data.ID, input, source, captured, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			raw := []byte("{\"source_id\":\"synthetic\",\"prompt\":\"Question\",\"completion\":\"Answer\"}\n")
			sum := sha256.Sum256(raw)
			digest := hex.EncodeToString(sum[:])
			if _, e = account.DB.Exec(ctx, "UPDATE mongodb_dataset_imports SET state='exported',source_sha256=$2,source_bytes=$3,source_rows=1,first_id='000000000000000000000001',last_id='000000000000000000000001' WHERE id=$1", receipt.ID, digest, len(raw)); e != nil {
				t.Fatal(e)
			}
			receipt, e = s.MongoDBGetImport(ctx, a, workspace, data.ID, receipt.ID)
			if e != nil {
				t.Fatal(e)
			}
			fenced := withMongoAttachment(ctx, receipt)
			if name == "ordinary_upload" {
				fenced = ctx
			}
			change := func() {
				t.Helper()
				if e := os.WriteFile(uri, []byte("mongodb://fixture:synthetic-second@127.0.0.1/training?authSource=admin"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if name == "changed_before_draft" {
				change()
			}
			draft, e := s.CreateDraft(fenced, a, workspace, data.ID, DraftRequest{SourceName: "snapshot.jsonl", SourceSHA256: digest, ExpectedBytes: int64(len(raw)), Reason: "Attach exported bytes"}, receipt.ID, uuid.NewString())
			if name == "changed_before_draft" {
				if e == nil || e.Error() != "MONGODB_SOURCE_CHANGED" || !reflect.ValueOf(draft).IsZero() {
					t.Fatal("changed source created a draft", e)
				}
				var drafts, audits int
				if e = account.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM dataset_drafts WHERE dataset_id=$1),(SELECT count(*) FROM audit_events WHERE action='dataset.draft_created')", data.ID).Scan(&drafts, &audits); e != nil || drafts != 0 || audits != 0 {
					t.Fatal("rejected draft left mutations", e, drafts, audits)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = s.mongoBindDraft(ctx, a, workspace, data.ID, receipt, draft.ID); e != nil {
				t.Fatal(e)
			}
			if name == "changed_before_upload" || name == "ordinary_upload" {
				change()
			}
			copied := false
			reader := &mongoAttachmentReader{Reader: bytes.NewReader(raw), before: func() {
				copied = true
				switch name {
				case "changed_during_upload":
					change()
				case "lease_expired_during_upload":
					_, e = account.DB.Exec(ctx, "UPDATE mongodb_dataset_imports SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", receipt.ID)
				case "attempt_replaced_during_upload":
					_, e = account.DB.Exec(ctx, "UPDATE mongodb_dataset_imports SET attempt_id=$2 WHERE id=$1", receipt.ID, uuid.NewString())
				case "membership_replaced_during_upload":
					_, e = account.DB.Exec(ctx, "DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", a.Me.Workspace.TenantID, workspace, a.Me.User.ID)
					if e == nil {
						_, e = account.DB.Exec(ctx, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", a.Me.Workspace.TenantID, workspace, a.Me.User.ID)
					}
				}
				if e != nil {
					t.Fatal(e)
				}
			}}
			out, err := s.Upload(fenced, a, workspace, data.ID, draft.ID, digest, int64(len(raw)), reader, uuid.NewString())
			success := name == "current_admin" || name == "current_portal" || name == "ordinary_upload"
			if success {
				if err != nil || out.State != "validating" || out.CurrentJobID == nil {
					t.Fatal("current/ordinary upload refused", err)
				}
			} else {
				code := "MONGODB_SOURCE_CHANGED"
				if name == "lease_expired_during_upload" || name == "attempt_replaced_during_upload" {
					code = "DATASET_ATTEMPT_STALE"
				}
				if err == nil || err.Error() != code || !reflect.ValueOf(out).IsZero() {
					t.Fatal("stale connector attachment admitted", err)
				}
			}
			var received bool
			var jobs, audits int
			var object string
			if e = account.DB.QueryRow(ctx, "SELECT source_received_at IS NOT NULL,source_object_id::text,(SELECT count(*) FROM dataset_jobs WHERE draft_id=$1),(SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='dataset.source_received') FROM dataset_drafts WHERE id=$1", draft.ID).Scan(&received, &object, &jobs, &audits); e != nil {
				t.Fatal(e)
			}
			want := 0
			if success {
				want = 1
			}
			if received != success || jobs != want || audits != want {
				t.Fatal("attachment/audit/job atomicity lost", received, jobs, audits)
			}
			if name == "changed_before_upload" && copied {
				t.Fatal("rejected upload copied bytes")
			}
			if copied {
				if _, e = store.Source(object, digest, int64(len(raw))); e != nil {
					t.Fatal("retained immutable bytes lost", e)
				}
			}
		})
	}
}
