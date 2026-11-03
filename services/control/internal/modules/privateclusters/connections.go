package privateclusters

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type ProfileChoice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	Namespace string `json:"namespace"`
}

func (s *Service) Profiles(ctx context.Context, a identity.Auth, workspace string) ([]ProfileChoice, error) {
	out := []ProfileChoice{}
	e := s.actor(ctx, a, workspace, true, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		for _, p := range s.config.Profiles {
			if p.WorkspaceID == w.ID {
				if e := installationWithin(ctx, q, p); e != nil {
					return e
				}
				out = append(out, ProfileChoice{p.ID, p.Name, p.Profile, p.Namespace})
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) Bind(ctx context.Context, a identity.Auth, m Mutation, pid string) (Connection, error) {
	var out Connection
	if m.validate() != nil || m.ExpectedRevision != 0 || !identifier.MatchString(pid) {
		return out, fail("VALIDATION_FAILED")
	}
	input := struct {
		Profile  string
		Mutation Mutation
	}{pid, m}
	e := s.actor(ctx, a, m.WorkspaceID, true, true, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		previous, replay, e := replayWithin(ctx, q, a, w, "bind", m, input)
		if e != nil {
			return e
		}
		if replay {
			out, e = connectionWithin(ctx, q, w.TenantID, w.ID, previous.target)
			return e
		}
		remote, e := s.client(ctx, w, pid)
		if e != nil {
			return e
		}
		defer remote.close()
		if e = installationWithin(ctx, q, remote.profile); e != nil {
			return e
		}
		observation, e := remote.observe(ctx)
		if e != nil {
			return e
		}
		if e = remote.empty(ctx, providerRun{}, false); e != nil {
			return e
		}
		p := remote.profile
		raw, _ := json.Marshal(observation)
		id := uuid.NewString()
		_, e = q.Exec(ctx, statement(q, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,observation,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'active',1,$15::jsonb,$16)`, `INSERT INTO private_cluster_connections(id,tenant_id,workspace_id,profile_id,cluster_uid,namespace_uid,namespace_name,installation_id,profile,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest,state,revision,observation,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'active',1,CAST(? AS JSON),?)`, id, w.TenantID, w.ID, pid, p.ClusterUID, p.NamespaceUID, p.Namespace, p.InstallationID, p.Profile, remote.configSHA, remote.credentialSHA, remote.approval.ManifestSHA256, remote.approval.Asset.ImageID, remote.approval.Asset.ImageConfigDigest, string(raw), a.Me.User.ID))
		if e != nil {
			return e
		}
		out, e = connectionWithin(ctx, q, w.TenantID, w.ID, id)
		if e != nil {
			return e
		}
		return recordIntent(ctx, q, a, w, "bind", id, out.Revision, m, input)
	})
	if e != nil {
		return Connection{}, e
	}
	return out, nil
}
func (s *Service) Change(ctx context.Context, a identity.Auth, m Mutation, id, operation string) (Connection, error) {
	var out Connection
	if m.validate() != nil || m.ExpectedRevision < 1 || !validID(id) || operation != "rebind" && operation != "disable" && operation != "observe" {
		return out, fail("VALIDATION_FAILED")
	}
	input := struct {
		Target, Operation string
		Mutation          Mutation
	}{id, operation, m}
	e := s.actor(ctx, a, m.WorkspaceID, true, true, operation != "disable", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		c, e := connectionWithin(ctx, q, w.TenantID, w.ID, id)
		if e != nil {
			return e
		}
		_, replay, e := replayWithin(ctx, q, a, w, operation, m, input)
		if e != nil {
			return e
		}
		if replay {
			out = c
			return nil
		}
		if c.Revision != m.ExpectedRevision {
			return fail("VERSION_CONFLICT")
		}
		if operation == "disable" {
			_, e = q.Exec(ctx, statement(q, `UPDATE private_cluster_connections SET state='disabled',revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND revision=$4`, `UPDATE private_cluster_connections SET state='disabled',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND revision=?`, w.TenantID, w.ID, id, c.Revision))
			if e != nil {
				return e
			}
		} else {
			remote, e := s.client(ctx, w, c.ProfileID)
			if e != nil {
				return e
			}
			defer remote.close()
			p := remote.profile
			if e = installationWithin(ctx, q, p); e != nil {
				return e
			}
			if c.clusterUID != p.ClusterUID || c.namespaceUID != p.NamespaceUID || c.Namespace != p.Namespace || c.Profile != p.Profile || c.installation != p.InstallationID {
				return fail("PRIVATE_CLUSTER_SCOPE_CHANGED")
			}
			if operation == "observe" {
				if e = c.bound(remote); e != nil {
					return e
				}
			} else if c.ActiveRunID != nil && c.configSHA != remote.configSHA {
				return fail("PRIVATE_CLUSTER_UNRESOLVED_RUN")
			}
			observation, e := remote.observe(ctx)
			if e != nil {
				return e
			}
			if c.ActiveRunID == nil {
				if e = remote.empty(ctx, providerRun{}, false); e != nil {
					return e
				}
			} else {
				r, e := runWithin(ctx, q, w.TenantID, w.ID, c.ID, *c.ActiveRunID)
				if e != nil {
					return e
				}
				if e = remote.recoveryInventory(ctx, r.provider()); e != nil {
					return e
				}
			}
			raw, _ := json.Marshal(observation)
			state := c.State
			if operation == "rebind" {
				state = "active"
			}
			_, e = q.Exec(ctx, statement(q, `UPDATE private_cluster_connections SET config_sha256=$1,credential_sha256=$2,manifest_sha256=$3,image_digest=$4,config_digest=$5,state=$6,observation=$7::jsonb,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$8 AND workspace_id=$9 AND id=$10 AND revision=$11`, `UPDATE private_cluster_connections SET config_sha256=?,credential_sha256=?,manifest_sha256=?,image_digest=?,config_digest=?,state=?,observation=CAST(? AS JSON),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND revision=?`, remote.configSHA, remote.credentialSHA, remote.approval.ManifestSHA256, remote.approval.Asset.ImageID, remote.approval.Asset.ImageConfigDigest, state, string(raw), w.TenantID, w.ID, id, c.Revision))
			if e != nil {
				return e
			}
		}
		out, e = connectionWithin(ctx, q, w.TenantID, w.ID, id)
		if e != nil {
			return e
		}
		return recordIntent(ctx, q, a, w, operation, id, out.Revision, m, input)
	})
	if e != nil {
		return Connection{}, e
	}
	return out, nil
}
