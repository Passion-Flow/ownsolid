package privateclusters

import (
	"context"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

func (s *Service) executor(q transaction.Executor) error {
	if s == nil || s.identity == nil || q == nil {
		return fail("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return e
	}
	if _, pool := q.(transaction.Store); pool || q.Dialect() != store.Dialect() {
		return fail("PRIVATE_CLUSTER_TRANSACTION_REQUIRED")
	}
	return nil
}

// InvalidateAuthoritiesWithin is a restore composition seam. It retains remote
// identities, unknown effects, immutable proofs and every active capacity fence.
// The actual restore owner must commit this with its other authority invalidation.
func (s *Service) InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if e := s.executor(q); e != nil {
		return e
	}
	return InvalidateAuthoritiesWithin(ctx, q)
}

// InvalidateAuthoritiesWithin fences persisted authority in an already owned
// restore transaction. It needs no provider credential, network access or enabled
// runtime service. Only an actual borrowed transaction is accepted; remote UIDs,
// active pointers, intent history and unresolved effects remain unchanged.
func InvalidateAuthoritiesWithin(ctx context.Context, q transaction.Executor) error {
	if ctx == nil || q == nil {
		return fail("PRIVATE_CLUSTER_TRANSACTION_REQUIRED")
	}
	if _, pool := q.(transaction.Store); pool || q.Dialect() != transaction.PostgreSQL && q.Dialect() != transaction.MySQL {
		return fail("PRIVATE_CLUSTER_TRANSACTION_REQUIRED")
	}
	_, e := q.Exec(ctx, statement(q, `UPDATE private_cluster_connections SET state='restore_fenced',revision=revision+1,updated_at=clock_timestamp() WHERE state<>'restore_fenced' AND revision<9007199254740991`, `UPDATE private_cluster_connections SET state='restore_fenced',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE state<>'restore_fenced' AND revision<9007199254740991`))
	if e != nil {
		return e
	}
	var unfenced bool
	if e = q.QueryRow(ctx, statement(q, `SELECT EXISTS(SELECT 1 FROM private_cluster_connections WHERE state<>'restore_fenced')`, `SELECT EXISTS(SELECT 1 FROM private_cluster_connections WHERE state<>'restore_fenced')`)).Scan(&unfenced); e != nil {
		return e
	}
	if unfenced {
		return fail("PRIVATE_CLUSTER_RESTORE_FENCE_FAILED")
	}
	return nil
}
func validRun(r Run, c Connection) error {
	if !validID(r.ID) || !validID(r.actor) || !validID(r.session) || r.epoch < 1 || r.Revision < 1 || r.Revision > MaximumRevision || r.connectionRevision < 1 || r.connectionRevision > MaximumRevision || len(r.tokenHash) != 32 || r.membership != nil && !validID(*r.membership) || r.ConnectionID != c.ID || r.tenant != c.tenant || r.WorkspaceID != c.WorkspaceID || !validHash(r.configSHA) || !validHash(r.request.SpecSHA) || r.request.Challenge.Validate() != nil || !validHash(r.request.ManifestSHA) || !strings.HasPrefix(r.request.ImageDigest, "sha256:") || !validHash(strings.TrimPrefix(r.request.ImageDigest, "sha256:")) || !strings.HasPrefix(r.request.ConfigDigest, "sha256:") || !validHash(strings.TrimPrefix(r.request.ConfigDigest, "sha256:")) || r.request.Profile != c.Profile || (Profile{Profile: c.Profile}).Architecture() != r.request.Challenge.Architecture || r.request.ID != r.ID || r.request.JobName != JobNamePrefix+r.ID || r.request.Challenge.RunID != r.ID || r.request.Challenge.NamespaceUID != c.namespaceUID || r.request.Challenge.InstallationID != c.installation {
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	for _, id := range []*string{r.LeaseUID, r.JobUID, r.PodUID} {
		if id != nil && !validID(*id) {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	}
	if r.JobUID != nil && r.LeaseUID == nil || r.PodUID != nil && r.JobUID == nil {
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	switch r.Phase {
	case "prepared", "lease_pending":
		if r.LeaseUID != nil || r.JobUID != nil {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	case "lease_acquired", "job_pending", "job_absent", "lease_delete_pending":
		if r.LeaseUID == nil {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	case "observing", "job_delete_pending":
		if r.LeaseUID == nil || r.JobUID == nil {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	case "cleaned":
		if r.Result == nil {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	default:
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	if r.Result != nil {
		switch *r.Result {
		case "succeeded":
			if r.Remote == nil || r.Remote.Proof == nil || r.PodUID == nil || r.Remote.ImageID != r.request.ConfigDigest || r.Remote.NodeName == "" || !strings.HasPrefix(r.Remote.ContainerID, "containerd://") {
				return fail("PRIVATE_CLUSTER_PROOF_MISSING")
			}
			verified, e := proof.Verify([]byte(r.Remote.Logs), r.request.Challenge, *r.PodUID)
			if e != nil || verified != *r.Remote.Proof {
				return fail("PRIVATE_CLUSTER_STORED_PROOF_INVALID")
			}
		case "failed", "cancelled":
		default:
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	}
	if r.Remote != nil {
		if r.Remote.Lease.UID != "" && (r.LeaseUID == nil || r.Remote.Lease.UID != *r.LeaseUID) || r.Remote.Job.UID != "" && (r.JobUID == nil || r.Remote.Job.UID != *r.JobUID) || r.Remote.PodUID != "" && (r.PodUID == nil || r.Remote.PodUID != *r.PodUID) {
			return fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
	}
	if r.Phase != "cleaned" && (c.ActiveRunID == nil || *c.ActiveRunID != r.ID || r.configSHA != c.configSHA) {
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	return nil
}

// integrityConnectionRows keeps at most one retained observation while nested
// run queries borrow the same transaction. This is a fixed streaming invariant,
// independent of the adjustable public page size.
const integrityConnectionRows = 1

// VerifyStoredWithin streams bounded keyset pages over the actual installed
// graph. It neither repairs rows nor requires expired historical sessions to
// exist, and never turns stored observations into fresh execution authority.
func (s *Service) VerifyStoredWithin(ctx context.Context, q transaction.Executor) error {
	if e := s.executor(q); e != nil {
		return e
	}
	if ctx == nil {
		return fail("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	var tenant string
	e := q.QueryRow(ctx, statement(q, `SELECT i.tenant_id::text FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' FOR SHARE`, `SELECT i.tenant_id FROM installation_state i JOIN tenants t ON t.id=i.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND t.status='active' FOR SHARE`)).Scan(&tenant)
	if e != nil {
		return e
	}
	after := "00000000-0000-0000-0000-000000000000"
	for {
		rows, e := q.Query(ctx, statement(q, "SELECT "+connectionPG+" FROM private_cluster_connections WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3", "SELECT "+connectionMY+" FROM private_cluster_connections WHERE tenant_id=? AND id>? ORDER BY id LIMIT ?", tenant, after, integrityConnectionRows))
		if e != nil {
			return e
		}
		page := []Connection{}
		for rows.Next() {
			c, e := scanConnection(rows)
			if e != nil {
				rows.Close()
				return e
			}
			page = append(page, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(page) == 0 {
			break
		}
		for _, c := range page {
			if !validID(c.ID) || !validID(c.WorkspaceID) || !validID(c.clusterUID) || !validID(c.namespaceUID) || !validID(c.installation) || c.Revision < 1 || c.Revision > MaximumRevision || !validHash(c.configSHA) || !validHash(c.credentialSHA) || !validHash(c.manifestSHA) || c.State != "active" && c.State != "disabled" && c.State != "restore_fenced" {
				return fail("PRIVATE_CLUSTER_STORED_INVALID")
			}
			runAfter := "00000000-0000-0000-0000-000000000000"
			activeFound := false
			for {
				rows, e := q.Query(ctx, statement(q, "SELECT "+runPG+" FROM private_cluster_runs WHERE tenant_id=$1 AND workspace_id=$2 AND connection_id=$3 AND id>$4 ORDER BY id LIMIT $5", "SELECT "+runMY+" FROM private_cluster_runs WHERE tenant_id=? AND workspace_id=? AND connection_id=? AND id>? ORDER BY id LIMIT ?", c.tenant, c.WorkspaceID, c.ID, runAfter, s.config.Policy.PageSize))
				if e != nil {
					return e
				}
				count := 0
				for rows.Next() {
					r, e := scanRun(rows)
					if e != nil {
						rows.Close()
						return e
					}
					if e = validRun(r, c); e != nil {
						rows.Close()
						return e
					}
					if c.ActiveRunID != nil && *c.ActiveRunID == r.ID {
						if r.Phase == "cleaned" {
							rows.Close()
							return fail("PRIVATE_CLUSTER_STORED_INVALID")
						}
						activeFound = true
					}
					count++
					runAfter = r.ID
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				if count == 0 {
					break
				}
			}
			if c.ActiveRunID != nil && !activeFound {
				return fail("PRIVATE_CLUSTER_STORED_INVALID")
			}
			after = c.ID
		}
	}
	var invalid bool
	e = q.QueryRow(ctx, statement(q, `SELECT EXISTS(SELECT 1 FROM private_cluster_intents i WHERE i.tenant_id=$1 AND ((i.operation IN('bind','rebind','disable','observe') AND NOT EXISTS(SELECT 1 FROM private_cluster_connections c WHERE c.tenant_id=i.tenant_id AND c.workspace_id=i.workspace_id AND c.id=i.target_id)) OR (i.operation NOT IN('bind','rebind','disable','observe') AND NOT EXISTS(SELECT 1 FROM private_cluster_runs r WHERE r.tenant_id=i.tenant_id AND r.workspace_id=i.workspace_id AND r.id=i.target_id))))`, `SELECT EXISTS(SELECT 1 FROM private_cluster_intents i WHERE i.tenant_id=? AND ((i.operation IN('bind','rebind','disable','observe') AND NOT EXISTS(SELECT 1 FROM private_cluster_connections c WHERE c.tenant_id=i.tenant_id AND c.workspace_id=i.workspace_id AND c.id=i.target_id)) OR (i.operation NOT IN('bind','rebind','disable','observe') AND NOT EXISTS(SELECT 1 FROM private_cluster_runs r WHERE r.tenant_id=i.tenant_id AND r.workspace_id=i.workspace_id AND r.id=i.target_id))))`, tenant)).Scan(&invalid)
	if e != nil {
		return e
	}
	if invalid {
		return fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	return nil
}
func (s *Service) VerifyStored(ctx context.Context) error {
	if s == nil || s.identity == nil || ctx == nil {
		return fail("PRIVATE_CLUSTER_OWNER_REQUIRED")
	}
	store, e := s.identity.Repository()
	if e != nil {
		return safeError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.timeout())
	defer cancel()
	return safeError(store.Within(ctx, false, s.VerifyStoredWithin))
}
func (c Config) timeout() time.Duration {
	return time.Duration(c.Policy.OperationSeconds) * time.Second
}
