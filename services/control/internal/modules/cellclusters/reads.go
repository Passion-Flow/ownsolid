package cellclusters

import (
	"context"
	"net/url"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

const clusterFields = `c.id::text,c.site_id,c.cell_id,c.name,c.distribution_version,c.ca_sha256,c.created_by::text,c.created_at,c.updated_at,
 s.node_id::text,s.state,
 count(*) FILTER (WHERE m.state='pending'),count(*) FILTER (WHERE m.state='joining'),count(*) FILTER (WHERE m.state='ready'),
 count(*) FILTER (WHERE m.state='not_ready'),count(*) FILTER (WHERE m.state='failed')`

const clusterFrom = ` FROM cell_clusters c JOIN cell_cluster_members s ON s.cluster_id=c.id AND s.role='server' JOIN cell_cluster_members m ON m.cluster_id=c.id`

const clusterGroup = ` GROUP BY c.id,s.node_id,s.state`

func scanCluster(row transaction.Row) (Cluster, error) {
	var c Cluster
	var counts MemberCounts
	e := row.Scan(&c.ID, &c.SiteID, &c.CellID, &c.Name, &c.Version, &c.CASHA256, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.ServerNode, &c.ServerState,
		&counts.Pending, &counts.Joining, &counts.Ready, &counts.NotReady, &counts.Failed)
	if e != nil {
		return Cluster{}, e
	}
	c.Members, c.State = counts, counts.State(c.ServerState)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, nil
}

// Get reads one cluster of this cell with its member counts.
func (s *Service) Get(ctx context.Context, a identity.Auth, cluster string) (Cluster, error) {
	if !canonical(cluster) {
		return Cluster{}, fail("VALIDATION_FAILED")
	}
	var out Cluster
	e := s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var e error
		out, e = scanCluster(q.QueryRow(ctx, pg(`SELECT `+clusterFields+clusterFrom+` WHERE c.id=$1 AND c.cell_id=local_cell_id()`+clusterGroup, cluster)))
		if e != nil {
			return fail("RESOURCE_NOT_FOUND")
		}
		return nil
	})
	return out, e
}

// List reads the clusters of this cell. A cell has at most one, so the list is one page.
func (s *Service) List(ctx context.Context, a identity.Auth) (Page[Cluster], error) {
	out := Page[Cluster]{Items: []Cluster{}}
	e := s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		rows, e := q.Query(ctx, pg(`SELECT `+clusterFields+clusterFrom+` WHERE c.cell_id=local_cell_id()`+clusterGroup+` ORDER BY c.created_at DESC,c.id DESC`))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			c, e := scanCluster(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, c)
		}
		return rows.Err()
	})
	return out, e
}

const memberFields = `m.node_id::text,n.name,m.workspace_id::text,m.role,host(m.node_address),m.state,m.error_code,m.task_id::text,m.join_attempts,m.observed_at,m.created_at,m.updated_at`

func scanMember(row transaction.Row) (Member, error) {
	var m Member
	e := row.Scan(&m.NodeID, &m.NodeName, &m.WorkspaceID, &m.Role, &m.Address, &m.State, &m.ErrorCode, &m.TaskID, &m.JoinAttempts, &m.ObservedAt, &m.CreatedAt, &m.UpdatedAt)
	if e == nil {
		m.CreatedAt, m.UpdatedAt = m.CreatedAt.UTC(), m.UpdatedAt.UTC()
		if m.ObservedAt != nil {
			observed := m.ObservedAt.UTC()
			m.ObservedAt = &observed
		}
	}
	return m, e
}

func memberWithin(ctx context.Context, q transaction.Executor, cluster, node string) (Member, error) {
	m, e := scanMember(q.QueryRow(ctx, pg(`SELECT `+memberFields+` FROM cell_cluster_members m JOIN managed_nodes n ON n.id=m.node_id WHERE m.cluster_id=$1 AND m.node_id=$2`, cluster, node)))
	if e != nil {
		return Member{}, fail("RESOURCE_NOT_FOUND")
	}
	return m, nil
}

var memberStates = map[string]bool{MemberPending: true, MemberJoining: true, MemberReady: true, MemberNotReady: true, MemberFailed: true}

// Members reads one page of a cluster's members in the order they were added, optionally
// only those in one state. The query takes state, limit and
// cursor.
func (s *Service) Members(ctx context.Context, a identity.Auth, cluster string, query url.Values) (Page[Member], error) {
	if !canonical(cluster) {
		return Page[Member]{}, fail("VALIDATION_FAILED")
	}
	values := url.Values{}
	state := ""
	for key, v := range query {
		if key == "state" {
			if len(v) != 1 || !memberStates[v[0]] {
				return Page[Member]{}, fail("VALIDATION_FAILED")
			}
			state = v[0]
			continue
		}
		values[key] = v
	}
	limit, cursor, scope, e := pagination.Parse(values, "cell-cluster-members:"+cluster+":"+state)
	if e != nil {
		return Page[Member]{}, e
	}
	out := Page[Member]{Items: []Member{}}
	e = s.adminWithin(ctx, a, func(ctx context.Context, q transaction.Executor) error {
		var exists bool
		if e := q.QueryRow(ctx, pg(`SELECT EXISTS(SELECT 1 FROM cell_clusters WHERE id=$1 AND cell_id=local_cell_id())`, cluster)).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return fail("RESOURCE_NOT_FOUND")
		}
		after := time.Time{}
		id := "00000000-0000-0000-0000-000000000000"
		if !cursor.Time.IsZero() {
			after, id = cursor.Time, cursor.ID
		}
		rows, e := q.Query(ctx, pg(`SELECT `+memberFields+` FROM cell_cluster_members m JOIN managed_nodes n ON n.id=m.node_id
			WHERE m.cluster_id=$1 AND ($2='' OR m.state=$2) AND (m.created_at,m.node_id)>($3,$4::uuid)
			ORDER BY m.created_at,m.node_id LIMIT $5`, cluster, state, after, id, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			m, e := scanMember(rows)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, m)
		}
		return rows.Err()
	})
	if e != nil {
		return Page[Member]{}, e
	}
	if len(out.Items) > limit {
		last := out.Items[limit-1]
		out.Items = out.Items[:limit]
		out.NextCursor = pagination.Encode(last.CreatedAt, last.NodeID, scope)
	}
	return out, nil
}
