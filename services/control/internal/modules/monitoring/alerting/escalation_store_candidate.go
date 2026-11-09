//go:build alerting_candidate

package alerting

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var occurrenceID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type escalationStore struct{ db *pgxpool.Pool }

// A caller builds the principal from authenticated server-side authority.
type acknowledgementPrincipal struct {
	occurrenceScope
	actor string
}

type acknowledgementReceipt struct {
	occurrenceScope
	id, actor string
	at        time.Time
}

func (s escalationStore) acknowledge(ctx context.Context, principal acknowledgementPrincipal, id string) (acknowledgementReceipt, error) {
	if !tenantID.MatchString(principal.tenant) || !relayLabel.MatchString(principal.workspace) ||
		!relayLabel.MatchString(principal.actor) || !occurrenceID.MatchString(id) {
		return acknowledgementReceipt{}, errors.New("ESCALATION_ACKNOWLEDGEMENT_INVALID")
	}
	out := acknowledgementReceipt{occurrenceScope: principal.occurrenceScope, id: id}
	// UPDATE locks the current row and rechecks the active predicate under
	// contention. COALESCE keeps the winning actor and timestamp on replay.
	err := s.db.QueryRow(ctx, `
UPDATE ownsolid_alert_candidate
SET ack_actor = COALESCE(ack_actor, $4), ack_at = COALESCE(ack_at, clock_timestamp())
WHERE tenant_id = $1 AND workspace_id = $2 AND occurrence_id = $3
  AND resolved_at IS NULL AND observed_at IS NOT NULL
RETURNING ack_actor, ack_at`, principal.tenant, principal.workspace, id, principal.actor).Scan(&out.actor, &out.at)
	if err != nil {
		return acknowledgementReceipt{}, err
	}
	return out, nil
}

func (s escalationStore) observe(ctx context.Context, o alertOccurrence, resolved bool, delay time.Duration) error {
	_, err := s.db.Exec(ctx, `
INSERT INTO ownsolid_alert_candidate AS existing
    (tenant_id, workspace_id, occurrence_id, fingerprint, started_at, observed_at, due_at, resolved_at)
VALUES ($1, $2, $3, $4, $5,
    CASE WHEN NOT $6::boolean THEN statement_timestamp() END,
    CASE WHEN NOT $6::boolean THEN statement_timestamp() + $7::bigint * INTERVAL '1 millisecond' END,
    CASE WHEN $6::boolean THEN clock_timestamp() END)
ON CONFLICT (tenant_id, workspace_id, occurrence_id)
DO UPDATE SET resolved_at = clock_timestamp()
WHERE existing.resolved_at IS NULL AND EXCLUDED.resolved_at IS NOT NULL`,
		o.tenant, o.workspace, o.id, o.fingerprint, o.started, resolved, delay.Milliseconds())
	return err
}

func (s escalationStore) admission(ctx context.Context, o alertOccurrence, limit int) (ready, terminal bool, err error) {
	err = s.db.QueryRow(ctx, `
SELECT COALESCE(due_at <= statement_timestamp(), false)
           AND (lease_until IS NULL OR lease_until <= statement_timestamp()),
       ack_at IS NOT NULL OR resolved_at IS NOT NULL OR sent_at IS NOT NULL OR rejected_at IS NOT NULL
           OR (attempt_no >= $4 AND (lease_until IS NULL OR lease_until <= statement_timestamp()))
FROM ownsolid_alert_candidate
WHERE tenant_id = $1 AND workspace_id = $2 AND occurrence_id = $3`, o.tenant, o.workspace, o.id, limit).Scan(&ready, &terminal)
	return
}

func (s escalationStore) claim(ctx context.Context, o alertOccurrence, policy escalationPolicy) (int, error) {
	var attempt int
	err := s.db.QueryRow(ctx, `
WITH acquired AS (
    UPDATE ownsolid_alert_candidate
    SET attempt_no = attempt_no + 1, lease_until = clock_timestamp() + $4::bigint * INTERVAL '1 millisecond'
    WHERE tenant_id = $1 AND workspace_id = $2 AND occurrence_id = $3
      AND ack_at IS NULL AND resolved_at IS NULL AND sent_at IS NULL AND rejected_at IS NULL
      AND due_at <= statement_timestamp() AND attempt_no < $5
      AND (lease_until IS NULL OR lease_until <= statement_timestamp())
    RETURNING tenant_id, workspace_id, occurrence_id, attempt_no, lease_until
)
INSERT INTO ownsolid_alert_attempt_candidate
    (tenant_id, workspace_id, occurrence_id, attempt_no, delivery_key, started_at, lease_until)
SELECT tenant_id, workspace_id, occurrence_id, attempt_no, $6, clock_timestamp(), lease_until
FROM acquired
RETURNING attempt_no`, o.tenant, o.workspace, o.id, policy.lease.Milliseconds(), policy.attemptLimit, o.deliveryKey()).Scan(&attempt)
	return attempt, err
}

func (s escalationStore) complete(ctx context.Context, o alertOccurrence, attempt int, result string) error {
	tag, err := s.db.Exec(ctx, `
WITH fenced AS (
    UPDATE ownsolid_alert_candidate
    SET lease_until = NULL,
        sent_at = CASE WHEN $5 = 'delivered' THEN clock_timestamp() ELSE sent_at END,
        rejected_at = CASE WHEN $5 = 'rejected' THEN clock_timestamp() ELSE rejected_at END
    WHERE tenant_id = $1 AND workspace_id = $2 AND occurrence_id = $3
      AND attempt_no = $4 AND lease_until IS NOT NULL
    RETURNING tenant_id, workspace_id, occurrence_id, attempt_no
)
UPDATE ownsolid_alert_attempt_candidate AS attempt
SET result = $5, completed_at = clock_timestamp()
FROM fenced
WHERE (attempt.tenant_id, attempt.workspace_id, attempt.occurrence_id, attempt.attempt_no) =
      (fenced.tenant_id, fenced.workspace_id, fenced.occurrence_id, fenced.attempt_no)`,
		o.tenant, o.workspace, o.id, attempt, result)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("ESCALATION_CANDIDATE_CLAIM_LOST")
	}
	return nil
}
