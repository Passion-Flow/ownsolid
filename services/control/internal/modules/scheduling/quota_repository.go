package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

var (
	// ErrQuotaRevisionConflict indicates the quota policy changed since it was read.
	ErrQuotaRevisionConflict = errors.New("SCHEDULING_QUOTA_REVISION_CONFLICT")
	// ErrQuotaPolicyUnavailable indicates a failed or invalid policy read or write.
	ErrQuotaPolicyUnavailable = errors.New("SCHEDULING_QUOTA_POLICY_UNAVAILABLE")
)

// ReadQuotaWithin reads and validates the stored policy in the caller's
// transaction. An unconfigured platform has revision zero and no policy.
func ReadQuotaWithin(ctx context.Context, tx pgx.Tx) (int64, *QuotaTree, error) {
	if tx == nil {
		return 0, nil, ErrQuotaPolicyUnavailable
	}
	var revision int64
	var data []byte
	err := tx.QueryRow(ctx, "SELECT revision, allocation FROM scheduling_quota_policy WHERE singleton").Scan(&revision, &data)
	if errors.Is(err, pgx.ErrNoRows) {
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
	if tx == nil {
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

	stmt := `UPDATE scheduling_quota_policy SET revision=revision+1, allocation=$1::jsonb
 WHERE singleton AND revision=$2 RETURNING revision`
	args := []any{data, expected}
	if expected == 0 {
		stmt = `INSERT INTO scheduling_quota_policy (singleton, revision, allocation)
 VALUES (true, 1, $1::jsonb) ON CONFLICT (singleton) DO NOTHING RETURNING revision`
		args = args[:1]
	}
	err = tx.QueryRow(ctx, stmt, args...).Scan(&revision)
	if err == nil {
		return revision, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, policyError(ctx)
	}
	var replay bool
	err = tx.QueryRow(ctx, `SELECT revision=$2 AND allocation=$1::jsonb
 FROM scheduling_quota_policy WHERE singleton`, data, expected+1).Scan(&replay)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, policyError(ctx)
	}
	if !replay {
		return 0, false, ErrQuotaRevisionConflict
	}
	return expected + 1, false, nil
}

func policyError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrQuotaPolicyUnavailable
}
