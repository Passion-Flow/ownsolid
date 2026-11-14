package monitoring

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// RefreshNodeScrapeInventory publishes the authorized node snapshot to a
// private, installation-owned file discovery directory. The protected policy
// and directory references are supplied by trusted server composition.
func (s *Service) RefreshNodeScrapeInventory(ctx context.Context, a identity.Auth, workspace, policyPath, directory, requestID string) (int, error) {
	if ctx == nil || !s.ready() {
		return 0, errors.New("MONITORING_OWNER_REQUIRED")
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return 0, errors.New("ACCESS_DENIED")
	}
	if !validID(workspace) || !validID(requestID) {
		return 0, errors.New("VALIDATION_FAILED")
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return 0, errors.New("MONITORING_BUSY")
	}
	run, cancel := context.WithTimeout(ctx, time.Duration(s.config.OperationSeconds)*time.Second)
	defer cancel()
	// Serialize the source read as well as rename. A slower publisher cannot
	// overwrite a newer snapshot after releasing the owning directory lock.
	root, dir, e := openScrapeDirectory(directory)
	if e != nil {
		return 0, e
	}
	defer root.Close()
	defer dir.Close()
	endpoints, e := readScrapePolicy(policyPath, workspace)
	if e != nil {
		return 0, e
	}
	store, e := s.identity.Repository()
	if e != nil {
		return 0, safeError(e)
	}
	if store.Dialect() != transaction.PostgreSQL {
		return 0, errors.New("MONITORING_SCRAPE_DIALECT_UNSUPPORTED")
	}
	groups := make([]scrapeTarget, 0, len(endpoints))
	e = store.Within(run, false, func(ctx context.Context, q transaction.Executor) error {
		w, e := s.authorizedWorkspaceWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if _, e := s.license.MonitoringWithin(ctx, q, a); e != nil {
			return e
		}
		// Keep a disabled Workspace address-free while letting its current
		// platform administrator withdraw the previous collector snapshot.
		if w.Status == "active" && len(endpoints) > 0 {
			groups, e = scrapeTargetsWithin(ctx, q, w, endpoints)
			if e != nil {
				return e
			}
		}
		return insertSnapshotAuditWithin(ctx, q, uuid.NewString(), a.Me.User.ID, w.ID, requestID, w.TenantID)
	})
	if e != nil {
		return 0, safeError(e)
	}
	if e := writeScrapeInventory(run, root, dir, workspace, groups); e != nil {
		return 0, e
	}
	return len(groups), nil
}

func scrapeTargetsWithin(ctx context.Context, q transaction.Executor, scope workspaces.Workspace, endpoints map[string]string) ([]scrapeTarget, error) {
	nodes := make([]string, 0, len(endpoints))
	for id := range endpoints {
		nodes = append(nodes, id)
	}
	rows, e := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `
		SELECT id::text,site_id,cell_id FROM managed_nodes
		WHERE tenant_id=$1 AND workspace_id=$2 AND status='active'
		  AND id=ANY(ARRAY(SELECT unnest($3::text[])::uuid))
		ORDER BY id`, scope.TenantID, scope.ID, nodes))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	groups := make([]scrapeTarget, 0, len(endpoints))
	for rows.Next() {
		var node, site, cell string
		if e := rows.Scan(&node, &site, &cell); e != nil {
			return nil, e
		}
		address := endpoints[node]
		if address == "" || !scrapePlacement.MatchString(site) || !scrapePlacement.MatchString(cell) {
			return nil, errors.New("MONITORING_SCRAPE_SCOPE_INVALID")
		}
		groups = append(groups, scrapeTarget{
			Targets: []string{address},
			Labels:  map[string]string{"node_id": node, "tenant_id": scope.TenantID, "workspace_id": scope.ID, "site_id": site, "cell_id": cell},
		})
	}
	return groups, rows.Err()
}
