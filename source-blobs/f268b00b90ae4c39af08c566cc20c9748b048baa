//go:build repository_integration

package datasets_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
)

// The first read is after the actual upload lease transaction has committed and
// before the source can finish copying. This gives a deterministic interleaving
// without sleeps, a second worker, or a fabricated processor result.
type uploadFenceReader struct {
	reader io.Reader
	before func() error
	err    error
}

func (r *uploadFenceReader) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		r.err = before()
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.Read(p)
}

func TestActualDatasetUploadFencesAcrossSourceCopy(t *testing.T) {
	for _, name := range []string{"current_portal", "current_admin", "membership_replaced", "cancelled", "lease_expired", "lease_cleared", "lease_replaced"} {
		t.Run(name, func(t *testing.T) {
			// Configure the existing real processor prerequisite, but do not start
			// it: this test stops at durable import admission, never ingestion.
			g := runtimeSetup(t, true)
			data := g.create(t)
			raw := runtimeRow("upload-fence", "Synthetic source copied across an authority boundary")
			sum := sha256.Sum256([]byte(raw))
			digest := hex.EncodeToString(sum[:])
			draft, e := g.s.CreateDraft(g.ctx, g.portal, g.f.Workspace, data.ID, datasets.DraftRequest{SourceName: "fence.jsonl", SourceSHA256: digest, ExpectedBytes: int64(len(raw)), Reason: "Exercise the exact upload lease"}, uuid.NewString(), uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			actor := g.portal
			if name == "current_admin" {
				actor = g.admin
			}
			var upload, object, originalAuthority string
			var observedLease bool
			newLease := uuid.NewString()
			reader := &uploadFenceReader{reader: strings.NewReader(raw)}
			reader.before = func() error {
				if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(
					"SELECT upload_attempt_id::text,source_object_id::text,state='uploading' AND source_received_at IS NULL FROM dataset_drafts WHERE id=$1",
					"SELECT upload_attempt_id,source_object_id,state='uploading' AND source_received_at IS NULL FROM dataset_drafts WHERE id=?", draft.ID)).Scan(&upload, &object, &observedLease); e != nil {
					return e
				}
				if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(
					"SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
					"SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", g.f.Tenant, g.f.Workspace, g.f.User)).Scan(&originalAuthority); e != nil {
					return e
				}
				switch name {
				case "membership_replaced":
					// Synthetic current-membership lifecycle; the actual upload owner
					// must distinguish removal/regrant even with the same user/role.
					if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(
						"DELETE FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
						"DELETE FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", g.f.Tenant, g.f.Workspace, g.f.User)); e != nil {
						return e
					}
					_, e := g.f.Store.Exec(g.ctx, g.f.Statement(
						"INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')",
						"INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", g.f.Tenant, g.f.Workspace, g.f.User))
					return e
				case "cancelled":
					_, e := g.s.CancelDraft(g.ctx, actor, g.f.Workspace, data.ID, draft.ID, "Cancel while the source is being copied", uuid.NewString())
					return e
				case "lease_expired":
					_, e := g.f.Store.Exec(g.ctx, g.f.Statement(
						"UPDATE dataset_drafts SET upload_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1",
						"UPDATE dataset_drafts SET upload_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", draft.ID))
					return e
				case "lease_cleared":
					_, e := g.f.Store.Exec(g.ctx, g.f.Statement(
						"UPDATE dataset_drafts SET upload_attempt_id=NULL,upload_expires_at=NULL WHERE id=$1",
						"UPDATE dataset_drafts SET upload_attempt_id=NULL,upload_expires_at=NULL WHERE id=?", draft.ID))
					return e
				case "lease_replaced":
					_, e := g.f.Store.Exec(g.ctx, g.f.Statement(
						"UPDATE dataset_drafts SET upload_attempt_id=$1 WHERE id=$2",
						"UPDATE dataset_drafts SET upload_attempt_id=? WHERE id=?", newLease, draft.ID))
					return e
				}
				return nil
			}
			out, e := g.s.Upload(g.ctx, actor, g.f.Workspace, data.ID, draft.ID, digest, int64(len(raw)), reader, uuid.NewString())
			if reader.err != nil || !observedLease || upload == "" {
				t.Fatal("source-copy interleaving was not reached", reader.err)
			}
			if _, check := g.s.Store.Source(object, digest, int64(len(raw))); check != nil {
				t.Fatal("immutable copied source was lost or changed", check)
			}
			success := name == "current_portal" || name == "current_admin"
			if success {
				if e != nil || out.State != "validating" || out.CurrentJobID == nil {
					t.Fatal("unchanged upload authority did not admit an import", e)
				}
			} else {
				runtimeError(t, e, "DATASET_ATTEMPT_STALE")
				if !reflect.ValueOf(out).IsZero() {
					t.Fatal("rejected upload projected uncommitted state")
				}
			}
			var state string
			var lease, job *string
			var received bool
			var audits int
			if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(
				"SELECT state,upload_attempt_id::text,current_job_id::text,source_received_at IS NOT NULL FROM dataset_drafts WHERE id=$1",
				"SELECT state,upload_attempt_id,current_job_id,source_received_at IS NOT NULL FROM dataset_drafts WHERE id=?", draft.ID)).Scan(&state, &lease, &job, &received); e != nil {
				t.Fatal(e)
			}
			if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(
				"SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='dataset.source_received'",
				"SELECT count(*) FROM audit_events WHERE target_id=? AND action='dataset.source_received'", draft.ID)).Scan(&audits); e != nil {
				t.Fatal(e)
			}
			jobs := g.count(t, "dataset_jobs")
			if success {
				if state != "validating" || lease != nil || job == nil || *job != *out.CurrentJobID || !received || jobs != 1 || audits != 1 {
					t.Fatal("successful source, lease, job and audit were not atomic")
				}
				replay, e := g.s.Upload(g.ctx, actor, g.f.Workspace, data.ID, draft.ID, digest, int64(len(raw)), strings.NewReader(""), uuid.NewString())
				if e != nil || replay.CurrentJobID == nil || *replay.CurrentJobID != *job || g.count(t, "dataset_jobs") != 1 {
					t.Fatal("source receipt replay created another import", e)
				}
			} else {
				if received || job != nil || jobs != 0 || audits != 0 || g.count(t, "dataset_rows") != 0 {
					t.Fatal("cancelled, stale or revoked upload admitted source/job/rows/audit")
				}
				switch name {
				case "cancelled":
					if state != "cancelled" || lease != nil {
						t.Fatal("late source copy overwrote durable cancellation")
					}
					_, e := g.s.Upload(g.ctx, actor, g.f.Workspace, data.ID, draft.ID, digest, int64(len(raw)), strings.NewReader(""), uuid.NewString())
					runtimeError(t, e, "DATASET_DRAFT_BUSY")
				case "lease_cleared":
					if state != "uploading" || lease != nil {
						t.Fatal("late source copy restored an invalidated lease")
					}
				case "lease_replaced":
					if state != "uploading" || lease == nil || *lease != newLease {
						t.Fatal("late source copy changed the replacement lease")
					}
				default:
					if state != "uploading" || lease == nil || *lease != upload {
						t.Fatal("failed final admission changed the retained upload receipt")
					}
				}
			}
			if name == "membership_replaced" {
				// Only an explicit new upload, after the original lease expires,
				// may reconcile the retained bytes with the replacement authority.
				g.exec(t,
					"UPDATE dataset_drafts SET upload_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1",
					"UPDATE dataset_drafts SET upload_expires_at=TIMESTAMPADD(SECOND,-1,CURRENT_TIMESTAMP(6)) WHERE id=?", draft.ID)
				recovered, e := g.s.Upload(g.ctx, actor, g.f.Workspace, data.ID, draft.ID, digest, int64(len(raw)), strings.NewReader(""), uuid.NewString())
				if e != nil || recovered.CurrentJobID == nil || recovered.State != "validating" || g.count(t, "dataset_jobs") != 1 {
					t.Fatal("explicit current-authority source reconciliation failed", e)
				}
				var accepted, current string
				if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(
					"SELECT membership_authority::text FROM dataset_jobs WHERE id=$1",
					"SELECT membership_authority FROM dataset_jobs WHERE id=?", *recovered.CurrentJobID)).Scan(&accepted); e != nil {
					t.Fatal(e)
				}
				if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(
					"SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3",
					"SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", g.f.Tenant, g.f.Workspace, g.f.User)).Scan(&current); e != nil || accepted != current || accepted == originalAuthority {
					t.Fatal("recovery did not bind the actual new membership lifetime", e)
				}
			}
		})
	}
}
