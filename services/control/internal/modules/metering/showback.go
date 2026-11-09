package metering

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ErrReportSnapshotRequired requires a read-only PostgreSQL REPEATABLE READ transaction.
var ErrReportSnapshotRequired = errors.New("METERING_REPORT_SNAPSHOT_REQUIRED")

// ShowbackLine contains one positive usage quantity per meter and rate
// version. Rate is nil and AmountMicros is empty for unrated usage.
// RateVersion is empty for usage before the first configured version.
type ShowbackLine struct {
	Meter        Meter
	RateVersion  string
	Quantity     string
	Rate         *MeterRate
	AmountMicros string
}

// ShowbackReport describes one authorized workspace and half-open period.
// KnownAmountMicros is the sum of rated line amounts. FullyRated indicates
// whether that subtotal covers all reported usage.
type ShowbackReport struct {
	Currency          string
	From              time.Time
	To                time.Time
	Lines             []ShowbackLine
	KnownAmountMicros string
	FullyRated        bool
}

// ShowbackWithin rates original occurrence times in one read-only REPEATABLE
// READ transaction. The caller supplies both scope IDs from its current
// read authorization and keeps the installation's historical rate versions.
func ShowbackWithin(ctx context.Context, tx pgx.Tx, scope Scope, from, to time.Time, card *RateCard) (ShowbackReport, error) {
	from, to, err := reportingWindow(scope, from, to)
	if err != nil {
		return ShowbackReport{}, err
	}
	if card == nil || len(card.versions) == 0 {
		return ShowbackReport{}, ErrInvalidRateCard
	}
	if err := requireReportSnapshot(ctx, tx); err != nil {
		return ShowbackReport{}, err
	}

	return readRatedReport(ctx, transaction.PostgreSQLTransaction(tx), scope, from, to, card)
}

func readRatedReport(ctx context.Context, q transaction.Executor, scope Scope, from, to time.Time, card *RateCard) (ShowbackReport, error) {
	first := sort.Search(len(card.versions), func(i int) bool { return card.versions[i].effective.After(from) }) - 1
	starts := []time.Time{from}
	for _, version := range card.versions[first+1:] {
		if !version.effective.Before(to) {
			break
		}
		starts = append(starts, version.effective)
	}

	// READ COMMITTED authority checks can precede the report. One statement
	// keeps every rate interval on the same ledger snapshot after those checks.
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT width_bucket(occurred_at, $5::timestamptz[]), `+usageTotalsProjection+`
 FROM usage_ledger WHERE tenant_id=$1 AND workspace_id=$2 AND occurred_at >= $3 AND occurred_at < $4
 GROUP BY 1 ORDER BY 1`, scope.TenantID, scope.WorkspaceID, from, to, starts))
	if err != nil {
		return ShowbackReport{}, operationError(ctx)
	}
	defer rows.Close()

	report := ShowbackReport{Currency: card.currency, From: from, To: to, FullyRated: true}
	var known big.Int
	for rows.Next() {
		var bucket int
		var totals Totals
		if err := rows.Scan(&bucket, &totals.GPUMicroseconds, &totals.Tokens, &totals.StorageByteSeconds, &totals.Requests); err != nil {
			return ShowbackReport{}, operationError(ctx)
		}
		if bucket < 1 || bucket > len(starts) {
			return ShowbackReport{}, ErrUnavailable
		}
		var version rateVersion
		if i := first + bucket - 1; i >= 0 {
			version = card.versions[i]
		}
		if err := report.addRatedTotals(version, totals, &known); err != nil {
			return ShowbackReport{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return ShowbackReport{}, operationError(ctx)
	}
	report.KnownAmountMicros = known.String()
	return report, nil
}

func requireReportSnapshot(ctx context.Context, tx pgx.Tx) error {
	if ctx == nil || tx == nil {
		return ErrReportSnapshotRequired
	}
	var snapshot bool
	if err := tx.QueryRow(ctx, `SELECT current_setting('transaction_read_only')='on'
 AND current_setting('transaction_isolation')='repeatable read'`).Scan(&snapshot); err != nil {
		return operationError(ctx)
	}
	if !snapshot {
		return ErrReportSnapshotRequired
	}
	return nil
}

func (report *ShowbackReport) addRatedTotals(version rateVersion, totals Totals, known *big.Int) error {
	for _, row := range []struct {
		meter    Meter
		quantity string
	}{
		{GPUMicroseconds, totals.GPUMicroseconds},
		{Tokens, totals.Tokens},
		{StorageByteSeconds, totals.StorageByteSeconds},
		{Requests, totals.Requests},
	} {
		quantity, ok := new(big.Int).SetString(row.quantity, 10)
		if !ok || quantity.Sign() < 0 {
			return ErrUnavailable
		}
		if quantity.Sign() == 0 {
			continue
		}
		line := ShowbackLine{Meter: row.meter, RateVersion: version.id, Quantity: row.quantity}
		if rate, exists := version.rates[row.meter]; exists {
			amount := roundedAmount(quantity, rate)
			known.Add(known, amount)
			line.Rate, line.AmountMicros = &rate, amount.String()
		} else {
			report.FullyRated = false
		}
		report.Lines = append(report.Lines, line)
	}
	return nil
}

func roundedAmount(quantity *big.Int, rate MeterRate) *big.Int {
	var amount, remainder big.Int
	denominator := big.NewInt(rate.Units)
	amount.Mul(quantity, big.NewInt(rate.AmountMicros))
	amount.QuoRem(&amount, denominator, &remainder)
	if remainder.Lsh(&remainder, 1).Cmp(denominator) >= 0 {
		amount.Add(&amount, big.NewInt(1))
	}
	return &amount
}
