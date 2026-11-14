package privateclusters

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Connection struct {
	ID                                                                                                               string       `json:"id"`
	WorkspaceID                                                                                                      string       `json:"workspace_id"`
	ProfileID                                                                                                        string       `json:"profile_id"`
	Profile                                                                                                          string       `json:"profile"`
	Namespace                                                                                                        string       `json:"namespace"`
	State                                                                                                            string       `json:"state"`
	Revision                                                                                                         int64        `json:"revision"`
	ActiveRunID                                                                                                      *string      `json:"active_run_id"`
	Observation                                                                                                      *Observation `json:"observation"`
	CreatedAt                                                                                                        time.Time    `json:"created_at"`
	UpdatedAt                                                                                                        time.Time    `json:"updated_at"`
	tenant, clusterUID, namespaceUID, installation, configSHA, credentialSHA, manifestSHA, imageDigest, configDigest string
}
type Run struct {
	ID                                string       `json:"id"`
	WorkspaceID                       string       `json:"workspace_id"`
	ConnectionID                      string       `json:"connection_id"`
	Phase                             string       `json:"phase"`
	Result                            *string      `json:"result"`
	Revision                          int64        `json:"revision"`
	LeaseUID                          *string      `json:"lease_uid"`
	JobUID                            *string      `json:"job_uid"`
	PodUID                            *string      `json:"pod_uid"`
	Remote                            *remoteState `json:"-"`
	CreatedAt                         time.Time    `json:"created_at"`
	UpdatedAt                         time.Time    `json:"updated_at"`
	tenant, actor, session, configSHA string
	epoch, connectionRevision         int64
	membership                        *string
	tokenHash                         []byte
	request                           providerRun
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

const connectionPG = `id::text,tenant_id::text,workspace_id::text,profile_id,profile,namespace_name,state,revision,active_run_id::text,observation,created_at,updated_at,cluster_uid::text,namespace_uid::text,installation_id::text,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest`
const connectionMY = `id,tenant_id,workspace_id,profile_id,profile,namespace_name,state,revision,active_run_id,observation,created_at,updated_at,cluster_uid,namespace_uid,installation_id,config_sha256,credential_sha256,manifest_sha256,image_digest,config_digest`
const runPG = `id::text,tenant_id::text,workspace_id::text,connection_id::text,phase,result,revision,lease_uid::text,job_uid::text,pod_uid::text,remote_state,created_at,updated_at,created_by::text,created_session_id::text,config_sha256,actor_epoch,connection_revision,membership_authority::text,created_token_hash,request,spec_sha256`
const runMY = `id,tenant_id,workspace_id,connection_id,phase,result,revision,lease_uid,job_uid,pod_uid,remote_state,created_at,updated_at,created_by,created_session_id,config_sha256,actor_epoch,connection_revision,membership_authority,created_token_hash,request,spec_sha256`

func scanConnection(row transaction.Row) (Connection, error) {
	var c Connection
	var observation []byte
	e := row.Scan(&c.ID, &c.tenant, &c.WorkspaceID, &c.ProfileID, &c.Profile, &c.Namespace, &c.State, &c.Revision, &c.ActiveRunID, &observation, &c.CreatedAt, &c.UpdatedAt, &c.clusterUID, &c.namespaceUID, &c.installation, &c.configSHA, &c.credentialSHA, &c.manifestSHA, &c.imageDigest, &c.configDigest)
	if e != nil {
		return Connection{}, e
	}
	if len(observation) > 0 {
		var o Observation
		if release.StrictJSON(observation, &o) != nil {
			return Connection{}, fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
		c.Observation = &o
	}
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}
func scanRun(row transaction.Row) (Run, error) {
	var r Run
	var request, remote []byte
	var spec string
	e := row.Scan(&r.ID, &r.tenant, &r.WorkspaceID, &r.ConnectionID, &r.Phase, &r.Result, &r.Revision, &r.LeaseUID, &r.JobUID, &r.PodUID, &remote, &r.CreatedAt, &r.UpdatedAt, &r.actor, &r.session, &r.configSHA, &r.epoch, &r.connectionRevision, &r.membership, &r.tokenHash, &request, &spec)
	if e != nil {
		return Run{}, e
	}
	if release.StrictJSON(request, &r.request) != nil || r.request.ID != r.ID || r.request.SpecSHA != spec || r.request.LeaseUID != "" || r.request.JobUID != "" || r.request.PodUID != "" {
		return Run{}, fail("PRIVATE_CLUSTER_STORED_INVALID")
	}
	if len(remote) > 0 {
		var v remoteState
		if release.StrictJSON(remote, &v) != nil {
			return Run{}, fail("PRIVATE_CLUSTER_STORED_INVALID")
		}
		r.Remote = &v
	}
	r.CreatedAt = r.CreatedAt.UTC()
	r.UpdatedAt = r.UpdatedAt.UTC()
	return r, nil
}
func connectionWithin(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Connection, error) {
	c, e := scanConnection(q.QueryRow(ctx, statement(q, "SELECT "+connectionPG+" FROM private_cluster_connections WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT "+connectionMY+" FROM private_cluster_connections WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", tenant, workspace, id)))
	if errors.Is(e, sql.ErrNoRows) {
		return Connection{}, fail("RESOURCE_NOT_FOUND")
	}
	return c, e
}
func runWithin(ctx context.Context, q transaction.Executor, tenant, workspace, connection, id string) (Run, error) {
	r, e := scanRun(q.QueryRow(ctx, statement(q, "SELECT "+runPG+" FROM private_cluster_runs WHERE tenant_id=$1 AND workspace_id=$2 AND connection_id=$3 AND id=$4 FOR UPDATE", "SELECT "+runMY+" FROM private_cluster_runs WHERE tenant_id=? AND workspace_id=? AND connection_id=? AND id=? FOR UPDATE", tenant, workspace, connection, id)))
	if errors.Is(e, sql.ErrNoRows) {
		return Run{}, fail("RESOURCE_NOT_FOUND")
	}
	return r, e
}
func (c Connection) bound(remote *client) error {
	p := remote.profile
	if c.configSHA != remote.configSHA || c.credentialSHA != remote.credentialSHA || c.clusterUID != p.ClusterUID || c.namespaceUID != p.NamespaceUID || c.Namespace != p.Namespace || c.Profile != p.Profile || c.installation != p.InstallationID || c.manifestSHA != remote.approval.ManifestSHA256 || c.imageDigest != remote.approval.Asset.ImageID || c.configDigest != remote.approval.Asset.ImageConfigDigest {
		return fail("PRIVATE_CLUSTER_REBIND_REQUIRED")
	}
	return nil
}
func (r Run) provider() providerRun {
	v := r.request
	if r.LeaseUID != nil {
		v.LeaseUID = *r.LeaseUID
	}
	if r.JobUID != nil {
		v.JobUID = *r.JobUID
	}
	if r.PodUID != nil {
		v.PodUID = *r.PodUID
	}
	return v
}
func saveRun(ctx context.Context, q transaction.Executor, r Run, phase string, result *string, remote *remoteState) (Run, error) {
	lease, job, pod := r.LeaseUID, r.JobUID, r.PodUID
	var raw any
	if remote != nil {
		b, e := json.Marshal(remote)
		if e != nil {
			return Run{}, e
		}
		raw = string(b)
		if remote.Lease.UID != "" {
			lease = &remote.Lease.UID
		}
		if remote.Job.UID != "" {
			job = &remote.Job.UID
		}
		if remote.PodUID != "" {
			pod = &remote.PodUID
		}
	}
	if r.Result != nil {
		result = r.Result
	}
	changed, e := q.Exec(ctx, statement(q, `UPDATE private_cluster_runs SET phase=$1,result=$2,lease_uid=$3,job_uid=$4,pod_uid=$5,remote_state=COALESCE($6::jsonb,remote_state),revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$7 AND workspace_id=$8 AND connection_id=$9 AND id=$10 AND revision=$11`, `UPDATE private_cluster_runs SET phase=?,result=?,lease_uid=?,job_uid=?,pod_uid=?,remote_state=COALESCE(CAST(? AS JSON),remote_state),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND connection_id=? AND id=? AND revision=?`, phase, result, lease, job, pod, raw, r.tenant, r.WorkspaceID, r.ConnectionID, r.ID, r.Revision))
	if e != nil {
		return Run{}, e
	}
	if changed != 1 {
		return Run{}, fail("VERSION_CONFLICT")
	}
	return runWithin(ctx, q, r.tenant, r.WorkspaceID, r.ConnectionID, r.ID)
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, id string) (Connection, error) {
	var out Connection
	if !validID(id) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	e := s.actor(ctx, a, workspace, false, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		var e error
		out, e = connectionWithin(ctx, q, w.TenantID, w.ID, id)
		return e
	})
	if e != nil {
		return Connection{}, e
	}
	return out, nil
}
func (s *Service) GetRun(ctx context.Context, a identity.Auth, workspace, connection, id string) (Run, error) {
	var out Run
	if !validID(connection) || !validID(id) {
		return out, fail("RESOURCE_NOT_FOUND")
	}
	e := s.actor(ctx, a, workspace, false, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		c, err := connectionWithin(ctx, q, w.TenantID, w.ID, connection)
		if err != nil {
			return err
		}
		var e error
		out, e = runWithin(ctx, q, w.TenantID, w.ID, connection, id)
		if e == nil {
			e = validRun(out, c)
		}
		return e
	})
	if e != nil {
		return Run{}, e
	}
	return out, nil
}

type cursor struct {
	Time string `json:"time"`
	ID   string `json:"id"`
}

func decodeCursor(raw string) (time.Time, string, error) {
	if raw == "" {
		return time.Time{}, "", nil
	}
	if len(raw) > 512 {
		return time.Time{}, "", fail("VALIDATION_FAILED")
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(raw)
	var c cursor
	if e != nil || release.StrictJSON(b, &c) != nil {
		return time.Time{}, "", fail("VALIDATION_FAILED")
	}
	t, e := time.Parse(time.RFC3339Nano, c.Time)
	canonical, _ := json.Marshal(c)
	if e != nil || t.Location() != time.UTC || c.Time != t.Format(time.RFC3339Nano) || !validID(c.ID) || base64.RawURLEncoding.EncodeToString(canonical) != raw {
		return time.Time{}, "", fail("VALIDATION_FAILED")
	}
	return t, c.ID, nil
}
func nextCursor(t time.Time, id string) *string {
	b, _ := json.Marshal(cursor{t.UTC().Format(time.RFC3339Nano), id})
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace, after string) (Page[Connection], error) {
	out := Page[Connection]{Items: []Connection{}}
	at, id, e := decodeCursor(after)
	if e != nil {
		return Page[Connection]{}, e
	}
	e = s.actor(ctx, a, workspace, false, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		var rows transaction.Rows
		var e error
		if after == "" {
			rows, e = q.Query(ctx, statement(q, "SELECT "+connectionPG+" FROM private_cluster_connections WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY created_at DESC,id DESC LIMIT $3", "SELECT "+connectionMY+" FROM private_cluster_connections WHERE tenant_id=? AND workspace_id=? ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, s.config.Policy.PageSize+1))
		} else {
			rows, e = q.Query(ctx, statement(q, "SELECT "+connectionPG+" FROM private_cluster_connections WHERE tenant_id=$1 AND workspace_id=$2 AND (created_at,id)<($3,$4) ORDER BY created_at DESC,id DESC LIMIT $5", "SELECT "+connectionMY+" FROM private_cluster_connections WHERE tenant_id=? AND workspace_id=? AND (created_at,id)<(?,?) ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, at, id, s.config.Policy.PageSize+1))
		}
		if e != nil {
			return e
		}
		defer rows.Close()
		budget, e := s.newPageBudget()
		if e != nil {
			return e
		}
		for rows.Next() {
			if len(out.Items) == s.config.Policy.PageSize {
				last := out.Items[len(out.Items)-1]
				out.NextCursor = nextCursor(last.CreatedAt, last.ID)
				break
			}
			c, e := scanConnection(rows)
			if e != nil {
				return e
			}
			if e = budget.add(c); e != nil {
				return e
			}
			out.Items = append(out.Items, c)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		return budget.finish(out.NextCursor)
	})
	if e != nil {
		return Page[Connection]{}, e
	}
	return out, nil
}
func (s *Service) Runs(ctx context.Context, a identity.Auth, workspace, connection, after string) (Page[Run], error) {
	out := Page[Run]{Items: []Run{}}
	at, id, e := decodeCursor(after)
	if e != nil || !validID(connection) {
		return Page[Run]{}, fail("VALIDATION_FAILED")
	}
	e = s.actor(ctx, a, workspace, false, false, false, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace, _ *string) error {
		if _, e := connectionWithin(ctx, q, w.TenantID, w.ID, connection); e != nil {
			return e
		}
		var rows transaction.Rows
		var e error
		if after == "" {
			rows, e = q.Query(ctx, statement(q, "SELECT "+runPG+" FROM private_cluster_runs WHERE tenant_id=$1 AND workspace_id=$2 AND connection_id=$3 ORDER BY created_at DESC,id DESC LIMIT $4", "SELECT "+runMY+" FROM private_cluster_runs WHERE tenant_id=? AND workspace_id=? AND connection_id=? ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, connection, s.config.Policy.PageSize+1))
		} else {
			rows, e = q.Query(ctx, statement(q, "SELECT "+runPG+" FROM private_cluster_runs WHERE tenant_id=$1 AND workspace_id=$2 AND connection_id=$3 AND (created_at,id)<($4,$5) ORDER BY created_at DESC,id DESC LIMIT $6", "SELECT "+runMY+" FROM private_cluster_runs WHERE tenant_id=? AND workspace_id=? AND connection_id=? AND (created_at,id)<(?,?) ORDER BY created_at DESC,id DESC LIMIT ?", w.TenantID, w.ID, connection, at, id, s.config.Policy.PageSize+1))
		}
		if e != nil {
			return e
		}
		defer rows.Close()
		budget, e := s.newPageBudget()
		if e != nil {
			return e
		}
		for rows.Next() {
			if len(out.Items) == s.config.Policy.PageSize {
				last := out.Items[len(out.Items)-1]
				out.NextCursor = nextCursor(last.CreatedAt, last.ID)
				break
			}
			r, e := scanRun(rows)
			if e != nil {
				return e
			}
			if e = budget.add(r); e != nil {
				return e
			}
			out.Items = append(out.Items, r)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		return budget.finish(out.NextCursor)
	})
	if e != nil {
		return Page[Run]{}, e
	}
	return out, nil
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}
