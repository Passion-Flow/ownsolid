package scheduling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrQuotaRevisionConflict indicates the quota policy changed since it was read.
	ErrQuotaRevisionConflict = errors.New("SCHEDULING_QUOTA_REVISION_CONFLICT")
	// ErrQuotaPolicyUnavailable indicates a failed or invalid policy read or write.
	ErrQuotaPolicyUnavailable = errors.New("SCHEDULING_QUOTA_POLICY_UNAVAILABLE")
	// ErrQuotaHasActiveReservations prevents a policy from stranding existing holds.
	ErrQuotaHasActiveReservations = errors.New("SCHEDULING_QUOTA_HAS_ACTIVE_RESERVATIONS")
)

// ReadQuotaWithin reads and validates the stored policy in the caller's
// transaction. An unconfigured platform has revision zero and no policy.
func ReadQuotaWithin(ctx context.Context, tx pgx.Tx) (int64, *QuotaTree, error) {
	if ctx == nil || tx == nil {
		return 0, nil, ErrQuotaPolicyUnavailable
	}
	return readQuotaPolicyWithin(ctx, transaction.PostgreSQLTransaction(tx))
}

func readQuotaPolicyWithin(ctx context.Context, q transaction.Executor) (int64, *QuotaTree, error) {
	var revision int64
	var data []byte
	err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT revision, allocation FROM scheduling_quota_policy WHERE singleton")).Scan(&revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, policyError(ctx)
	}
	var nodes []QuotaNode
	if json.Unmarshal(data, &nodes) != nil {
		return 0, nil, ErrQuotaPolicyUnavailable
	}
	tree, err := BuildQuotaTree(nodes)
	if err != nil {
		return 0, nil, ErrQuotaPolicyUnavailable
	}
	return revision, tree, nil
}

// ReplaceQuotaWithin validates and atomically replaces an operator-authorized
// allocation in its transaction. Zero means create. An exact replay against
// the immediately preceding revision returns the same new revision with changed
// set to false. Audit a change and commit it with the policy before acknowledging.
func ReplaceQuotaWithin(ctx context.Context, tx pgx.Tx, expected int64, nodes []QuotaNode) (revision int64, changed bool, err error) {
	if ctx == nil || tx == nil {
		return 0, false, ErrQuotaPolicyUnavailable
	}
	if expected < 0 || expected == math.MaxInt64 {
		return 0, false, ErrQuotaRevisionConflict
	}
	tree, err := BuildQuotaTree(nodes)
	if err != nil {
		return 0, false, err
	}
	ordered := make([]QuotaNode, 0, len(tree.nodes))
	for _, node := range tree.nodes {
		ordered = append(ordered, node)
	}
	slices.SortFunc(ordered, func(a, b QuotaNode) int { return strings.Compare(a.ID, b.ID) })
	data, err := json.Marshal(ordered)
	if err != nil {
		return 0, false, ErrInvalidQuotaTree
	}
	q := transaction.PostgreSQLTransaction(tx)
	if err := requireReservationTransaction(ctx, q); err != nil {
		return 0, false, err
	}
	revision, err = writeQuotaWithin(ctx, q, expected, tree, data)
	if err == nil {
		return revision, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	var replay bool
	err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT revision=$2 AND allocation=$1::jsonb FROM scheduling_quota_policy WHERE singleton`, data, expected+1)).Scan(&replay)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, policyError(ctx)
	}
	if !replay {
		return 0, false, ErrQuotaRevisionConflict
	}
	return expected + 1, false, nil
}

func writeQuotaWithin(ctx context.Context, q transaction.Executor, expected int64, tree *QuotaTree, data []byte) (int64, error) {
	var revision int64
	if expected == 0 {
		err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
			`INSERT INTO scheduling_quota_policy (singleton, revision, allocation)
 VALUES (true, 1, $1::jsonb) ON CONFLICT (singleton) DO NOTHING RETURNING revision`, data)).Scan(&revision)
		if err == nil {
			err = checkBorrowingPolicyWithin(ctx, q, nil, tree)
		}
		return revision, quotaWriteError(ctx, err)
	}
	// Do not read active usage until earlier shared policy readers have finished.
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT revision FROM scheduling_quota_policy WHERE singleton AND revision=$1 FOR UPDATE", expected)).Scan(&revision); err != nil {
		return 0, quotaWriteError(ctx, err)
	}
	_, previous, err := readQuotaPolicyWithin(ctx, q)
	if err != nil {
		return 0, err
	}
	if err := checkBorrowingPolicyWithin(ctx, q, previous, tree); err != nil {
		return 0, err
	}
	if err := checkActivePolicyWithin(ctx, q, tree); err != nil {
		return 0, err
	}
	err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		`UPDATE scheduling_quota_policy SET revision=revision+1, allocation=$1::jsonb
 WHERE singleton AND revision=$2 RETURNING revision`, data, expected)).Scan(&revision)
	return revision, quotaWriteError(ctx, err)
}

func checkActivePolicyWithin(ctx context.Context, q transaction.Executor, tree *QuotaTree) error {
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT s.tenant_id::text,s.workspace_id::text,r.key,sum(r.value::numeric)::text,
 bool_and((s.site_id=local_site_id() AND s.cell_id=local_cell_id()) IS TRUE)
 FROM scheduling_reservations s CROSS JOIN LATERAL jsonb_each_text(s.demand) r
 WHERE s.released_at IS NULL GROUP BY s.tenant_id,s.workspace_id,r.key`))
	if err != nil {
		return policyError(ctx)
	}
	defer rows.Close()
	for rows.Next() {
		var tenant, workspace, raw string
		var resource Resource
		var local bool
		if err := rows.Scan(&tenant, &workspace, &resource, &raw, &local); err != nil {
			return policyError(ctx)
		}
		if !local {
			return ErrQuotaPolicyUnavailable
		}
		usage, err := parseReservationUsage(resource, raw)
		if err != nil {
			return err
		}
		node, err := tree.workspace(tenant, workspace)
		if err != nil || usage > node.Limits.Burst[resource] {
			return ErrQuotaHasActiveReservations
		}
	}
	if err := rows.Err(); err != nil {
		return policyError(ctx)
	}
	return nil
}

func quotaWriteError(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return policyError(ctx)
}

func policyError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrQuotaPolicyUnavailable
}
