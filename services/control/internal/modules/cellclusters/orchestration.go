package cellclusters

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// openMember is a member whose work is not settled, as the reconciler reads it.
type openMember struct {
	node, workspace, role, address, state, requestedBy string
	task                                               *string
}

// clusterRow is the part of a cluster the reconciler needs.
type clusterRow struct {
	id, tenant, version string
	ca                  *string
	tokenExpires        *time.Time
	tokenSealed         []byte
	tokenTask           *string
	createdBy           string
}

func readClusterWithin(ctx context.Context, q transaction.Executor, tenant, cluster string) (clusterRow, error) {
	c := clusterRow{id: cluster, tenant: tenant}
	e := q.QueryRow(ctx, pg(`SELECT distribution_version,ca_sha256,join_token_expires_at,join_token_sealed,token_task_id::text,created_by::text FROM cell_clusters WHERE tenant_id=$1 AND id=$2`, tenant, cluster)).Scan(&c.version, &c.ca, &c.tokenExpires, &c.tokenSealed, &c.tokenTask, &c.createdBy)
	return c, e
}

// taskOutcome reads a node task's state wherever it is kept: live, or in the history once
// pruned. A task that is gone from both was lost.
func taskOutcome(ctx context.Context, q transaction.Executor, task string) (state, code string, e error) {
	var errorCode *string
	e = q.QueryRow(ctx, pg(`SELECT state,error_code FROM node_tasks WHERE id=$1 UNION ALL SELECT state,error_code FROM node_task_history WHERE id=$1 LIMIT 1`, task)).Scan(&state, &errorCode)
	if e != nil {
		return "", "", e
	}
	if errorCode != nil {
		code = *errorCode
	}
	return state, code, nil
}

func finished(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

// lostTaskCode is what a member records when its task ended without a result.
func lostTaskCode(code string) string {
	if code == "" {
		return "KUBERNETES_TASK_LOST"
	}
	return code
}

// command is what a member's task asks of its node, without the join token, which the
// task row never stores.
func (c clusterRow) command(m openMember, server string) wire.KubernetesCommand {
	out := wire.KubernetesCommand{ClusterID: c.id, Version: c.version, Role: m.role, NodeName: m.node, NodeAddress: m.address}
	if m.role == wire.KubernetesWorkerRole {
		out.ServerAddress = server
	}
	return out
}

// advanceWithin moves one locked cluster on: it settles members whose task ended without a
// result, joins the server, asks the server for a join token while workers wait, and joins
// workers in batches. Busy nodes stay pending and are tried on the next pass.
func (s *Service) advanceWithin(ctx context.Context, q transaction.Executor, tenant, cluster, request string) error {
	c, e := readClusterWithin(ctx, q, tenant, cluster)
	if e != nil {
		return e
	}
	members, e := openMembersWithin(ctx, q, cluster)
	if e != nil {
		return e
	}
	var server *openMember
	if server, e = serverWithin(ctx, q, cluster); e != nil {
		return e
	}
	for i := range members {
		m := &members[i]
		if m.state != MemberJoining {
			continue
		}
		state, code, e := taskOutcome(ctx, q, *m.task)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil && !finished(state) {
			continue
		}
		code = lostTaskCode(code)
		if e = s.settleWithin(ctx, q, tenant, cluster, *m, MemberFailed, &code, request); e != nil {
			return e
		}
		m.state = MemberFailed
		if m.role == wire.KubernetesServerRole {
			server.state = MemberFailed
		}
	}
	if server.state == MemberPending {
		_, e = s.joinWithin(ctx, q, c, *server, "", request)
		return e
	}
	if server.state != MemberReady || c.ca == nil {
		return nil
	}
	waiting, joining := []openMember{}, 0
	for _, m := range members {
		if m.role != wire.KubernetesWorkerRole {
			continue
		}
		switch m.state {
		case MemberPending:
			waiting = append(waiting, m)
		case MemberJoining:
			joining++
		}
	}
	if len(waiting) == 0 {
		return nil
	}
	ready, e := s.tokenWithin(ctx, q, c, *server, request)
	if e != nil || !ready {
		return e
	}
	for _, m := range waiting {
		if joining >= s.config.JoinBatch {
			break
		}
		started, e := s.joinWithin(ctx, q, c, m, server.address, request)
		if e != nil {
			return e
		}
		if started {
			joining++
		}
	}
	return nil
}

// tokenWithin reports whether the cluster holds a join token that outlives a join started
// now and still opens under the current key. Otherwise it asks the server for one, unless
// it already did, and settles a token task that ended without a result: the waiting
// workers fail with its error, so an administrator sees why and retries them.
func (s *Service) tokenWithin(ctx context.Context, q transaction.Executor, c clusterRow, server openMember, request string) (bool, error) {
	margin := time.Duration(s.config.JoinSeconds+s.config.ReconcileSeconds) * time.Second
	if c.tokenExpires != nil && c.tokenExpires.After(s.now().Add(margin)) {
		if _, e := s.sealer.open(c.id, c.tokenSealed); e == nil {
			return true, nil
		}
	}
	if c.tokenTask != nil {
		state, code, e := taskOutcome(ctx, q, *c.tokenTask)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return false, e
		}
		if e == nil && !finished(state) {
			return false, nil
		}
		return false, s.tokenFailedWithin(ctx, q, c, lostTaskCode(code), request)
	}
	command := c.command(server, "")
	command.TokenTTLSeconds = s.config.TokenTTLSeconds
	task, e := s.Nodes.CreateKubernetesTaskWithin(ctx, q, c.createdBy, c.tenant, server.workspace, server.node, wire.KubernetesTokenOperation, command, s.config.JoinSeconds, request)
	if e != nil {
		if e.Error() == "NODE_BUSY" {
			return false, nil
		}
		return false, e
	}
	_, e = q.Exec(ctx, pg(`UPDATE cell_clusters SET token_task_id=$2,updated_at=clock_timestamp() WHERE id=$1`, c.id, task.ID))
	return false, e
}

// tokenFailedWithin ends a token request that failed: the waiting workers fail with its
// code and the cluster may ask again once one is retried.
func (s *Service) tokenFailedWithin(ctx context.Context, q transaction.Executor, c clusterRow, code, request string) error {
	if _, e := q.Exec(ctx, pg(`UPDATE cell_clusters SET token_task_id=NULL,updated_at=clock_timestamp() WHERE id=$1`, c.id)); e != nil {
		return e
	}
	rows, e := q.Query(ctx, pg(`SELECT node_id::text,workspace_id::text,role,host(node_address),state,requested_by::text,task_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND role='worker' AND state='pending' ORDER BY node_id`, c.id))
	if e != nil {
		return e
	}
	waiting, e := scanOpenMembers(rows)
	if e != nil {
		return e
	}
	for _, m := range waiting {
		if e = s.settleWithin(ctx, q, c.tenant, c.id, m, MemberFailed, &code, request); e != nil {
			return e
		}
	}
	return nil
}

// joinWithin writes a member's join task and reports whether it did. A busy node stays
// pending; a node that is gone or offline fails at once, so the administrator sees it
// instead of waiting for a deadline.
func (s *Service) joinWithin(ctx context.Context, q transaction.Executor, c clusterRow, m openMember, server, request string) (bool, error) {
	task, e := s.Nodes.CreateKubernetesTaskWithin(ctx, q, m.requestedBy, c.tenant, m.workspace, m.node, wire.KubernetesJoinOperation, c.command(m, server), s.config.JoinSeconds, request)
	switch {
	case e != nil && e.Error() == "NODE_BUSY":
		return false, nil
	case e != nil && (e.Error() == "NODE_UNAVAILABLE" || e.Error() == "RESOURCE_NOT_FOUND" || e.Error() == "AGENT_PROFILE_REJECTED"):
		code := e.Error()
		if code == "RESOURCE_NOT_FOUND" {
			code = "NODE_UNAVAILABLE"
		}
		return false, s.settleWithin(ctx, q, c.tenant, c.id, m, MemberFailed, &code, request)
	case e != nil:
		return false, e
	}
	if _, e = q.Exec(ctx, pg(`UPDATE cell_cluster_members SET state='joining',task_id=$3,join_attempts=join_attempts+1,error_code=NULL,updated_at=clock_timestamp() WHERE cluster_id=$1 AND node_id=$2`, c.id, m.node, task.ID)); e != nil {
		return false, e
	}
	return true, s.auditWithin(ctx, q, "", "system", c.tenant, m.workspace, "cell_cluster.member_join_requested", "node", m.node, "", request)
}

// settleWithin records a member's state after its task, and audits a join's outcome.
func (s *Service) settleWithin(ctx context.Context, q transaction.Executor, tenant, cluster string, m openMember, state string, code *string, request string) error {
	if _, e := q.Exec(ctx, pg(`UPDATE cell_cluster_members SET state=$3::varchar,error_code=$4,task_id=NULL,observed_at=CASE WHEN $3::varchar IN ('ready','not_ready') THEN clock_timestamp() ELSE observed_at END,updated_at=clock_timestamp() WHERE cluster_id=$1 AND node_id=$2`, cluster, m.node, state, code)); e != nil {
		return e
	}
	action := ""
	switch {
	case state == MemberFailed:
		action = "cell_cluster.member_failed"
	case state == MemberReady && m.state == MemberJoining:
		action = "cell_cluster.member_joined"
	}
	if action == "" {
		return nil
	}
	return s.auditWithin(ctx, q, "", "system", tenant, m.workspace, action, "node", m.node, "", request)
}

// observeWithin writes an observe task for each settled member whose node is idle.
func (s *Service) observeWithin(ctx context.Context, q transaction.Executor, actor, cluster, request string) error {
	tenant, e := s.lockClusterWithin(ctx, q, cluster)
	if e != nil {
		return e
	}
	c, e := readClusterWithin(ctx, q, tenant, cluster)
	if e != nil {
		return e
	}
	rows, e := q.Query(ctx, pg(`SELECT node_id::text,workspace_id::text,role,host(node_address),state,requested_by::text,task_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND state IN ('ready','not_ready') AND task_id IS NULL ORDER BY node_id`, cluster))
	if e != nil {
		return e
	}
	settled, e := scanOpenMembers(rows)
	if e != nil {
		return e
	}
	for _, m := range settled {
		task, e := s.Nodes.CreateKubernetesTaskWithin(ctx, q, actor, tenant, m.workspace, m.node, wire.KubernetesObserveOperation, c.command(m, ""), s.Identity.Config.Policy().Nodes.TaskSeconds, request)
		if e != nil && (e.Error() == "NODE_BUSY" || e.Error() == "NODE_UNAVAILABLE") {
			continue
		}
		if e != nil {
			return e
		}
		if _, e = q.Exec(ctx, pg(`UPDATE cell_cluster_members SET task_id=$3,updated_at=clock_timestamp() WHERE cluster_id=$1 AND node_id=$2`, cluster, m.node, task.ID)); e != nil {
			return e
		}
	}
	return nil
}

func scanOpenMembers(rows transaction.Rows) ([]openMember, error) {
	defer rows.Close()
	out := []openMember{}
	for rows.Next() {
		var m openMember
		if e := rows.Scan(&m.node, &m.workspace, &m.role, &m.address, &m.state, &m.requestedBy, &m.task); e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// openMembersWithin reads the members that wait or join, plus the server, server first.
func openMembersWithin(ctx context.Context, q transaction.Executor, cluster string) ([]openMember, error) {
	rows, e := q.Query(ctx, pg(`SELECT node_id::text,workspace_id::text,role,host(node_address),state,requested_by::text,task_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND state IN ('pending','joining') ORDER BY role='worker',created_at,node_id`, cluster))
	if e != nil {
		return nil, e
	}
	return scanOpenMembers(rows)
}

func serverWithin(ctx context.Context, q transaction.Executor, cluster string) (*openMember, error) {
	rows, e := q.Query(ctx, pg(`SELECT node_id::text,workspace_id::text,role,host(node_address),state,requested_by::text,task_id::text FROM cell_cluster_members WHERE cluster_id=$1 AND role='server'`, cluster))
	if e != nil {
		return nil, e
	}
	found, e := scanOpenMembers(rows)
	if e != nil {
		return nil, e
	}
	if len(found) != 1 {
		return nil, fail("STORAGE_UNAVAILABLE")
	}
	return &found[0], nil
}

// Reconcile advances every cluster of this cell that still has work. Each cluster moves in
// its own transaction, and one locked by another process is skipped until the next pass.
func (s *Service) Reconcile(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var clusters []string
	if e := s.within(ctx, s.Identity.Config.Policy().Nodes.ReconcileTimeoutSeconds, func(ctx context.Context, q transaction.Executor) error {
		rows, e := q.Query(ctx, pg(`SELECT DISTINCT m.cluster_id::text FROM cell_cluster_members m JOIN cell_clusters c ON c.id=m.cluster_id WHERE c.cell_id=local_cell_id() AND m.state IN ('pending','joining') ORDER BY 1`))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				return e
			}
			clusters = append(clusters, id)
		}
		return rows.Err()
	}); e != nil {
		return e
	}
	for _, cluster := range clusters {
		request := uuid.NewString()
		if e := s.within(ctx, s.Identity.Config.Policy().HTTP.RequestSeconds, func(ctx context.Context, q transaction.Executor) error {
			var tenant string
			e := q.QueryRow(ctx, pg(`SELECT tenant_id::text FROM cell_clusters WHERE id=$1 FOR UPDATE SKIP LOCKED`, cluster)).Scan(&tenant)
			if e != nil {
				// Another process holds it, or it is gone: either way, not this pass.
				return nil
			}
			return s.advanceWithin(ctx, q, tenant, cluster, request)
		}); e != nil {
			if ctx.Err() != nil {
				return e
			}
			// One cluster's failure must not hold back the others.
			slog.Warn("cell_cluster_advance_failed", "cluster_id", cluster, "code", e.Error())
		}
	}
	return nil
}

// Run reconciles until ctx ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(config.Seconds(s.config.ReconcileSeconds))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if e := s.Reconcile(ctx); e != nil && ctx.Err() == nil {
				slog.Warn("cell_cluster_reconcile_failed", "code", e.Error())
			}
		}
	}
}
