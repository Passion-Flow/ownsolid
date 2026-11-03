package privateclusters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

func (s *Service) Start(ctx context.Context, a identity.Auth, m Mutation, connection string) (Run, error) {
	var out Run
	created := false
	if m.validate() != nil || m.ExpectedRevision < 1 || !validID(connection) {
		return out, fail("VALIDATION_FAILED")
	}
	input := struct {
		Connection string
		Mutation   Mutation
	}{connection, m}
	e := s.actor(ctx, a, m.WorkspaceID, false, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, authority *string) error {
		c, e := connectionWithin(ctx, q, w.TenantID, w.ID, connection)
		if e != nil {
			return e
		}
		previous, replay, e := replayWithin(ctx, q, a, w, "start", m, input)
		if e != nil {
			return e
		}
		if replay {
			out, e = runWithin(ctx, q, w.TenantID, w.ID, connection, previous.target)
			return e
		}
		if c.State != "active" || c.ActiveRunID != nil {
			return fail("PRIVATE_CLUSTER_UNRESOLVED_OR_DISABLED")
		}
		if c.Revision != m.ExpectedRevision {
			return fail("VERSION_CONFLICT")
		}
		remote, e := s.client(ctx, w, c.ProfileID)
		if e != nil {
			return e
		}
		defer remote.close()
		if e = c.bound(remote); e != nil {
			return e
		}
		if e = installationWithin(ctx, q, remote.profile); e != nil {
			return e
		}
		if _, e = remote.observe(ctx); e != nil {
			return e
		}
		if e = remote.empty(ctx, providerRun{}, false); e != nil {
			return e
		}
		id := uuid.NewString()
		var entropy [32]byte
		if _, e = rand.Read(entropy[:]); e != nil {
			return fail("PRIVATE_CLUSTER_ENTROPY_UNAVAILABLE")
		}
		request, e := remote.prepared(id, proof.Challenge{FormatVersion: 1, RunID: id, NamespaceUID: c.namespaceUID, InstallationID: c.installation, Nonce: hex.EncodeToString(entropy[:]), Architecture: remote.profile.Architecture(), RunAsUser: remote.policy.RunAsUser})
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(request)
		_, e = q.Exec(ctx, statement(q, `INSERT INTO private_cluster_runs(id,tenant_id,workspace_id,connection_id,connection_revision,created_by,actor_epoch,membership_authority,created_session_id,created_token_hash,config_sha256,request,spec_sha256,phase,revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,'prepared',1)`, `INSERT INTO private_cluster_runs(id,tenant_id,workspace_id,connection_id,connection_revision,created_by,actor_epoch,membership_authority,created_session_id,created_token_hash,config_sha256,request,spec_sha256,phase,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,CAST(? AS JSON),?,'prepared',1)`, id, w.TenantID, w.ID, connection, c.Revision, a.Me.User.ID, a.Epoch, authority, a.SessionID, a.TokenHash, c.configSHA, string(raw), request.SpecSHA))
		if e != nil {
			return e
		}
		changed, e := q.Exec(ctx, statement(q, `UPDATE private_cluster_connections SET active_run_id=$1,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4 AND active_run_id IS NULL AND revision=$5`, `UPDATE private_cluster_connections SET active_run_id=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND active_run_id IS NULL AND revision=?`, id, w.TenantID, w.ID, connection, c.Revision))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("VERSION_CONFLICT")
		}
		out, e = runWithin(ctx, q, w.TenantID, w.ID, connection, id)
		if e != nil {
			return e
		}
		created = true
		return recordIntent(ctx, q, a, w, "start", id, out.Revision, m, input)
	})
	if e != nil {
		return Run{}, e
	}
	if !created {
		return out, nil
	}
	// Known committed stages advance synchronously. Any error stops this call. A
	// repeated Start key only reads its recorded run and never resends a mutation.
	for step := 0; step < 2; step++ {
		out, e = s.planCreate(ctx, a, m, connection, out.ID)
		if e != nil {
			return Run{}, e
		}
		out, e = s.effect(ctx, a, m, connection, out.ID, out.Revision, true)
		if e != nil {
			return Run{}, e
		}
	}
	return out, nil
}
func (s *Service) lockedRun(ctx context.Context, q transaction.Executor, w workspaces.Workspace, connection, id string) (Connection, Run, error) {
	c, e := connectionWithin(ctx, q, w.TenantID, w.ID, connection)
	if e != nil {
		return Connection{}, Run{}, e
	}
	r, e := runWithin(ctx, q, w.TenantID, w.ID, connection, id)
	if e != nil {
		return Connection{}, Run{}, e
	}
	if r.Phase != "cleaned" && (c.ActiveRunID == nil || *c.ActiveRunID != r.ID) {
		return Connection{}, Run{}, fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	return c, r, nil
}
func (s *Service) remoteWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, c Connection, r Run) (*client, error) {
	remote, e := s.client(ctx, w, c.ProfileID)
	if e != nil {
		return nil, e
	}
	if e = c.bound(remote); e == nil {
		e = installationWithin(ctx, q, remote.profile)
	}
	if e == nil && r.configSHA != c.configSHA {
		e = fail("PRIVATE_CLUSTER_RUN_SPEC_CHANGED")
	}
	if e != nil {
		remote.close()
		return nil, e
	}
	return remote, nil
}
func (s *Service) planCreate(ctx context.Context, a identity.Auth, m Mutation, connection, id string) (Run, error) {
	var out Run
	e := s.actor(ctx, a, m.WorkspaceID, false, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, authority *string) error {
		c, r, e := s.lockedRun(ctx, q, w, connection, id)
		if e != nil {
			return e
		}
		if e = original(r, a, authority); e != nil {
			return e
		}
		if c.State != "active" {
			return fail("PRIVATE_CLUSTER_DISABLED")
		}
		remote, e := s.remoteWithin(ctx, q, w, c, r)
		if e != nil {
			return e
		}
		defer remote.close()
		if e = remote.scope(ctx); e != nil {
			return e
		}
		phase := ""
		switch r.Phase {
		case "prepared":
			phase = "lease_pending"
			if e = remote.empty(ctx, r.provider(), false); e != nil {
				return e
			}
		case "lease_acquired":
			phase = "job_pending"
			if e = remote.empty(ctx, r.provider(), true); e != nil {
				return e
			}
		default:
			return fail("PRIVATE_CLUSTER_RECONCILIATION_REQUIRED")
		}
		out, e = saveRun(ctx, q, r, phase, nil, nil)
		if e != nil {
			return e
		}
		return auditWithin(ctx, q, a, w, "intent."+phase, id, m)
	})
	if e != nil {
		return Run{}, e
	}
	return out, nil
}
func knownRejected(e error) bool {
	var remote *apiError
	if !errors.As(e, &remote) || remote.unknown {
		return false
	}
	return remote.code == "PRIVATE_CLUSTER_AUTHENTICATION_REFUSED" || remote.code == "PRIVATE_CLUSTER_ACCESS_FORBIDDEN"
}
func (s *Service) effect(ctx context.Context, a identity.Auth, m Mutation, connection, id string, revision int64, creation bool) (Run, error) {
	var out Run
	var rejected error
	// Current license admission is checked below only for creation. Safety deletion
	// remains available to current authority after new-business expiry.
	e := s.actor(ctx, a, m.WorkspaceID, false, true, creation, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, authority *string) error {
		c, r, e := s.lockedRun(ctx, q, w, connection, id)
		if e != nil {
			return e
		}
		if r.Revision != revision {
			return fail("VERSION_CONFLICT")
		}
		if creation != (r.Phase == "lease_pending" || r.Phase == "job_pending") {
			return fail("PRIVATE_CLUSTER_RECONCILIATION_REQUIRED")
		}
		if creation {
			if e = original(r, a, authority); e != nil {
				return e
			}
			if c.State != "active" {
				return fail("PRIVATE_CLUSTER_DISABLED")
			}
		} else if a.Me.Audience != "admin" {
			if e = original(r, a, authority); e != nil {
				return e
			}
		}
		remote, e := s.remoteWithin(ctx, q, w, c, r)
		if e != nil {
			return e
		}
		defer remote.close()
		if e = remote.scope(ctx); e != nil {
			return e
		}
		if creation {
			if e = remote.access(ctx); e != nil {
				return e
			}
			if _, _, e = remote.policies(ctx); e != nil {
				return e
			}
		}
		phase := r.Phase
		observed := remoteState{State: r.Phase}
		var previous string
		switch r.Phase {
		case "lease_pending":
			previous = "prepared"
			observed.Lease, e = remote.createLease(ctx, r.provider())
			phase = "lease_acquired"
		case "job_pending":
			previous = "lease_acquired"
			observed.Lease = remoteRef{UID: *r.LeaseUID}
			observed.Job, e = remote.createJob(ctx, r.provider())
			phase = "observing"
		case "job_delete_pending":
			e = remote.deleteJob(ctx, r.provider())
		case "lease_delete_pending":
			e = remote.deleteLease(ctx, r.provider())
		default:
			return fail("PRIVATE_CLUSTER_RECONCILIATION_REQUIRED")
		}
		if e != nil {
			if creation && knownRejected(e) {
				rejected = e
				out, e = saveRun(ctx, q, r, previous, nil, nil)
				if e != nil {
					return e
				}
				return auditWithin(ctx, q, a, w, "rejected."+previous, id, m)
			}
			return e
		}
		if creation {
			out, e = saveRun(ctx, q, r, phase, nil, &observed)
			if e != nil {
				return e
			}
		} else {
			out = r
		}
		// Deletion acceptance is audited but its pending phase is retained until GET
		// observes full absence. A 200/202 body never grants released capacity.
		return auditWithin(ctx, q, a, w, "effect."+r.Phase, id, m)
	})
	if e != nil {
		return Run{}, e
	}
	if rejected != nil {
		return Run{}, safeError(rejected)
	}
	return out, nil
}
func (s *Service) Action(ctx context.Context, a identity.Auth, m Mutation, connection, id, operation string) (Run, error) {
	var out Run
	execute := false
	if m.validate() != nil || m.ExpectedRevision < 1 || !validID(connection) || !validID(id) || operation != "continue" && operation != "reconcile" && operation != "cancel" && operation != "cleanup" {
		return out, fail("VALIDATION_FAILED")
	}
	input := struct {
		Connection, Run, Operation string
		Mutation                   Mutation
	}{connection, id, operation, m}
	e := s.actor(ctx, a, m.WorkspaceID, false, true, operation == "continue", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, authority *string) error {
		c, r, e := s.lockedRun(ctx, q, w, connection, id)
		if e != nil {
			return e
		}
		_, replay, e := replayWithin(ctx, q, a, w, operation, m, input)
		if e != nil {
			return e
		}
		if replay {
			out = r
			return nil
		}
		if r.Revision != m.ExpectedRevision {
			return fail("VERSION_CONFLICT")
		}
		if operation == "continue" || a.Me.Audience != "admin" {
			if e = original(r, a, authority); e != nil {
				return e
			}
		}
		remote, e := s.remoteWithin(ctx, q, w, c, r)
		if e != nil {
			return e
		}
		defer remote.close()
		if e = remote.scope(ctx); e != nil {
			return e
		}
		switch operation {
		case "continue":
			if c.State != "active" {
				return fail("PRIVATE_CLUSTER_DISABLED")
			}
			switch r.Phase {
			case "prepared":
				if e = remote.empty(ctx, r.provider(), false); e != nil {
					return e
				}
				out, e = saveRun(ctx, q, r, "lease_pending", nil, nil)
				execute = true
			case "lease_acquired":
				if e = remote.empty(ctx, r.provider(), true); e != nil {
					return e
				}
				out, e = saveRun(ctx, q, r, "job_pending", nil, nil)
				execute = true
			default:
				return fail("PRIVATE_CLUSTER_RECONCILIATION_REQUIRED")
			}
		case "reconcile":
			out, e = s.reconcileWithin(ctx, q, w, c, r, remote)
		case "cancel", "cleanup":
			result := r.Result
			if result == nil {
				v := "cancelled"
				if operation == "cleanup" {
					return fail("PRIVATE_CLUSTER_CANCEL_OR_TERMINAL_REQUIRED")
				}
				result = &v
			}
			switch r.Phase {
			case "prepared":
				out, e = saveRun(ctx, q, r, "cleaned", result, nil)
				if e == nil {
					e = releaseActive(ctx, q, c, r.ID)
				}
			case "lease_acquired":
				out, e = saveRun(ctx, q, r, "job_absent", result, nil)
			case "observing":
				out, e = saveRun(ctx, q, r, "job_delete_pending", result, nil)
				execute = true
			case "job_absent":
				if e = remote.empty(ctx, r.provider(), true); e != nil {
					return e
				}
				out, e = saveRun(ctx, q, r, "lease_delete_pending", result, nil)
				execute = true
			case "job_delete_pending", "lease_delete_pending":
				return fail("PRIVATE_CLUSTER_RECONCILIATION_REQUIRED")
			case "cleaned":
				out = r
			default:
				return fail("PRIVATE_CLUSTER_UNKNOWN_CREATE_REQUIRES_OBSERVATION")
			}
		}
		if e != nil {
			return e
		}
		return recordIntent(ctx, q, a, w, operation, id, out.Revision, m, input)
	})
	if e != nil {
		return Run{}, e
	}
	if execute {
		return s.effect(ctx, a, m, connection, id, out.Revision, operation == "continue")
	}
	return out, nil
}
func releaseActive(ctx context.Context, q transaction.Executor, c Connection, id string) error {
	changed, e := q.Exec(ctx, statement(q, `UPDATE private_cluster_connections SET active_run_id=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND active_run_id=$4`, `UPDATE private_cluster_connections SET active_run_id=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND active_run_id=?`, c.tenant, c.WorkspaceID, c.ID, id))
	if e != nil {
		return e
	}
	if changed != 1 {
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	return nil
}
func (s *Service) reconcileWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, c Connection, r Run, remote *client) (Run, error) {
	switch r.Phase {
	case "prepared", "lease_acquired", "cleaned":
		return r, nil
	case "lease_pending":
		ref, e := remote.lease(ctx, r.provider())
		if missing(e) {
			return Run{}, fail("PRIVATE_CLUSTER_UNKNOWN_CREATE_UNRESOLVED")
		}
		if e != nil {
			return Run{}, e
		}
		return saveRun(ctx, q, r, "lease_acquired", nil, &remoteState{Lease: ref, State: "lease_acquired"})
	case "job_pending", "observing":
		observed, e := remote.state(ctx, r.provider())
		if missing(e) {
			return Run{}, fail("PRIVATE_CLUSTER_UNKNOWN_OR_LOST_JOB")
		}
		if e != nil {
			return Run{}, e
		}
		var result *string
		if observed.State == "succeeded" || observed.State == "failed" {
			result = &observed.State
		}
		if r.Result != nil && result != nil && *result != *r.Result {
			return Run{}, fail("PRIVATE_CLUSTER_TERMINAL_CHANGED")
		}
		return saveRun(ctx, q, r, "observing", result, &observed)
	case "job_delete_pending":
		jobs, pods, e := remote.inventory(ctx)
		if e != nil {
			return Run{}, e
		}
		if len(jobs) != 0 || len(pods) != 0 {
			deleting, err := remote.deletionInventory(ctx, r.provider())
			if err != nil {
				return Run{}, err
			}
			if deleting {
				return r, nil
			}
			return saveRun(ctx, q, r, "observing", nil, nil)
		}
		if _, e = remote.lease(ctx, r.provider()); e != nil {
			return Run{}, e
		}
		return saveRun(ctx, q, r, "job_absent", nil, nil)
	case "job_absent":
		if e := remote.empty(ctx, r.provider(), true); e != nil {
			return Run{}, e
		}
		return r, nil
	case "lease_delete_pending":
		absent, e := remote.absent(ctx, r.provider())
		if e != nil && e.Error() == "PRIVATE_CLUSTER_DELETION_IN_PROGRESS" {
			return r, nil
		}
		if e != nil {
			return Run{}, e
		}
		if !absent {
			return saveRun(ctx, q, r, "job_absent", nil, nil)
		}
		out, e := saveRun(ctx, q, r, "cleaned", nil, nil)
		if e != nil {
			return Run{}, e
		}
		if e = releaseActive(ctx, q, c, r.ID); e != nil {
			return Run{}, e
		}
		return out, nil
	}
	return Run{}, fail("PRIVATE_CLUSTER_STORED_INVALID")
}
