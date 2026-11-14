package sso

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

// This owner exists only in the external overlay, with no route or worker admission.
type directoryCandidate struct {
	s                                                   *Service
	RunSeconds, FreshSeconds, MaxGroups, MaxMemberships int
	phase                                               func(string)
}
type directoryRun struct {
	ID, State, Error                string
	CreatedAt, RunUntil, ValidUntil time.Time
}
type directoryGroups map[string][]string
type directoryCurrent struct {
	Run    directoryRun
	Groups directoryGroups
}

const directoryRunColumns = `id::text,state,COALESCE(error_code,''),created_at,run_until,valid_until`

func scanDirectoryRun(q transaction.Row) (directoryRun, error) {
	var r directoryRun
	err := q.Scan(&r.ID, &r.State, &r.Error, &r.CreatedAt, &r.RunUntil, &r.ValidUntil)
	r.CreatedAt = r.CreatedAt.UTC()
	r.RunUntil = r.RunUntil.UTC()
	r.ValidUntil = r.ValidUntil.UTC()
	return r, err
}
func (d *directoryCandidate) ready(a identity.Auth) error {
	if !platformAdministrator(a) {
		return fail("ACCESS_DENIED")
	}
	if d == nil || d.s == nil || d.RunSeconds < 1 || d.RunSeconds > 60 || d.FreshSeconds <= d.RunSeconds || d.FreshSeconds > 3600 || d.MaxGroups < 1 || d.MaxGroups > 10000 || d.MaxMemberships < 1 || d.MaxMemberships > 100000 {
		return fail("DIRECTORY_CONFIGURATION_INVALID")
	}
	return nil
}
func (d *directoryCandidate) within(ctx context.Context, a identity.Auth, connector string, fn func(context.Context, transaction.Executor, string, Connector) error) error {
	if err := d.ready(a); err != nil {
		return err
	}
	if ctx == nil || !canonicalID(connector) {
		return fail("VALIDATION_FAILED")
	}
	return d.s.actorTransaction(ctx, a, connectorReadFence, connectorExclusive, func(_ context.Context, _ transaction.Executor, tenant string) ([]connectorLock, error) {
		return []connectorLock{{tenant, connector}}, nil
	}, func(ctx context.Context, q transaction.Executor, tenant string) error {
		if q.Dialect() != transaction.PostgreSQL {
			return fail("DIRECTORY_CONFIGURATION_INVALID")
		}
		c, err := selectConnectorWithin(ctx, q, tenant, connector)
		if errors.Is(err, sql.ErrNoRows) {
			return fail("SSO_CONNECTOR_NOT_FOUND")
		}
		if err != nil {
			return err
		}
		return fn(ctx, q, tenant, c)
	})
}
func (d *directoryCandidate) Start(ctx context.Context, a identity.Auth, connector string, revision int64, key, request string) (directoryRun, error) {
	if err := d.ready(a); err != nil {
		return directoryRun{}, err
	}
	if !canonicalID(key) || !canonicalID(request) || revision < 1 {
		return directoryRun{}, fail("VALIDATION_FAILED")
	}
	fingerprint, err := d.s.fingerprint("directory_sync", connector, struct {
		Revision int64
		Session  string
		Epoch    int64
	}{revision, a.SessionID, a.Epoch})
	if err != nil {
		return directoryRun{}, err
	}
	var out directoryRun
	err = d.within(ctx, a, connector, func(ctx context.Context, q transaction.Executor, tenant string, c Connector) error {
		if !c.Enabled || c.Revision != revision {
			return fail("DIRECTORY_SOURCE_CHANGED")
		}
		var oldConnector, oldSession string
		var oldEpoch int64
		var oldFingerprint []byte
		err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT connector_id::text,actor_session_id::text,actor_epoch,fingerprint FROM iam_directory_runs WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, tenant, a.Me.User.ID, key)).Scan(&oldConnector, &oldSession, &oldEpoch, &oldFingerprint)
		if err == nil {
			if oldConnector != connector || oldSession != a.SessionID || oldEpoch != a.Epoch || !hmac.Equal(oldFingerprint, fingerprint) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			var e error
			out, e = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+directoryRunColumns+` FROM iam_directory_runs WHERE tenant_id=$1 AND actor_user_id=$2 AND idempotency_key=$3`, tenant, a.Me.User.ID, key)))
			return e
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		expired := sha256.Sum256([]byte("failed:DIRECTORY_RUN_EXPIRED"))
		changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE iam_directory_runs SET state='failed',error_code='DIRECTORY_RUN_EXPIRED',completion_digest=$3,finished_at=clock_timestamp() WHERE tenant_id=$1 AND connector_id=$2 AND state='running' AND run_until<=clock_timestamp()`, tenant, connector, expired[:]))
		if err != nil {
			return err
		}
		if changed > 0 {
			if err = audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", "sso.directory_sync_failed", "failure", "sso_connector", connector, "expired", request); err != nil {
				return err
			}
		}
		var count int
		if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND state='running'`, tenant, connector)).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fail("DIRECTORY_SYNC_IN_PROGRESS")
		}
		id := uuid.NewString()
		out, err = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `WITH moment AS (SELECT clock_timestamp() AS now) INSERT INTO iam_directory_runs(id,tenant_id,connector_id,connector_revision,auth_version,actor_user_id,actor_session_id,actor_epoch,idempotency_key,fingerprint,state,created_at,run_until,valid_until) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'running',now,now+$11::bigint*interval '1 second',now+$12::bigint*interval '1 second' FROM moment RETURNING `+directoryRunColumns, id, tenant, connector, c.Revision, c.AuthVersion, a.Me.User.ID, a.SessionID, a.Epoch, key, fingerprint, d.RunSeconds, d.FreshSeconds)))
		if err != nil {
			return err
		}
		return audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", "sso.directory_sync_started", "accepted", "sso_connector", connector, "manual", request)
	})
	if err != nil {
		return directoryRun{}, err
	}
	return out, nil
}
func (d *directoryCandidate) validate(groups directoryGroups, failed bool) ([]byte, error) {
	if failed {
		if groups != nil {
			return nil, fail("VALIDATION_FAILED")
		}
		sum := sha256.Sum256([]byte("failed:DIRECTORY_SCAN_FAILED"))
		return sum[:], nil
	}
	if groups == nil || len(groups) > d.MaxGroups {
		return nil, fail("VALIDATION_FAILED")
	}
	canonical := make(directoryGroups, len(groups))
	total := 0
	for group, members := range groups {
		if !canonicalID(group) {
			return nil, fail("VALIDATION_FAILED")
		}
		m := slices.Clone(members)
		slices.Sort(m)
		for i, user := range m {
			total++
			if !canonicalID(user) || (i > 0 && user == m[i-1]) || total > d.MaxMemberships {
				return nil, fail("VALIDATION_FAILED")
			}
		}
		canonical[group] = m
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}
func (d *directoryCandidate) Finish(ctx context.Context, a identity.Auth, connector, run string, groups directoryGroups, failed bool, request string) (directoryRun, error) {
	if err := d.ready(a); err != nil {
		return directoryRun{}, err
	}
	if !canonicalID(run) || !canonicalID(request) {
		return directoryRun{}, fail("VALIDATION_FAILED")
	}
	digest, err := d.validate(groups, failed)
	if err != nil {
		return directoryRun{}, err
	}
	var out directoryRun
	err = d.within(ctx, a, connector, func(ctx context.Context, q transaction.Executor, tenant string, c Connector) error {
		var revision, version int64
		var previous []byte
		err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT connector_revision,auth_version,completion_digest FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND actor_user_id=$4 AND actor_session_id=$5 AND actor_epoch=$6 FOR UPDATE`, tenant, connector, run, a.Me.User.ID, a.SessionID, a.Epoch)).Scan(&revision, &version, &previous)
		if errors.Is(err, sql.ErrNoRows) {
			return fail("DIRECTORY_RUN_NOT_FOUND")
		}
		if err != nil {
			return err
		}
		if !c.Enabled || c.Revision != revision || c.AuthVersion != version {
			return fail("DIRECTORY_SOURCE_CHANGED")
		}
		out, err = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+directoryRunColumns+` FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3`, tenant, connector, run)))
		if err != nil {
			return err
		}
		if out.State != "running" {
			if !hmac.Equal(previous, digest) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			return nil
		}
		var fresh bool
		if err = q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT run_until>clock_timestamp() FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3`, tenant, connector, run)).Scan(&fresh); err != nil {
			return err
		}
		if !fresh {
			return fail("DIRECTORY_RUN_EXPIRED")
		}
		state, code, action, outcome := "succeeded", any(nil), "sso.directory_sync_completed", "success"
		if failed {
			state, code, action, outcome = "failed", "DIRECTORY_SCAN_FAILED", "sso.directory_sync_failed", "failure"
		} else {
			ids := make([]string, 0, len(groups))
			for id := range groups {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			for _, group := range ids {
				if _, err = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO iam_directory_groups(tenant_id,connector_id,run_id,external_group_id) VALUES($1,$2,$3,$4)`, tenant, connector, run, group)); err != nil {
					return err
				}
				members := slices.Clone(groups[group])
				slices.Sort(members)
				for _, user := range members {
					if _, err = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO iam_directory_members(tenant_id,connector_id,run_id,external_group_id,external_user_id) VALUES($1,$2,$3,$4,$5)`, tenant, connector, run, group, user)); err != nil {
						return err
					}
				}
			}
		}
		if d.phase != nil {
			d.phase("staged")
		}
		changed, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE iam_directory_runs SET state=$4,error_code=$5,completion_digest=$6,finished_at=clock_timestamp() WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND run_until>clock_timestamp()`, tenant, connector, run, state, code, digest))
		if err != nil {
			return err
		}
		if changed != 1 {
			return fail("DIRECTORY_RUN_EXPIRED")
		}
		if !failed {
			if _, err = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO iam_directory_heads(tenant_id,connector_id,run_id) VALUES($1,$2,$3) ON CONFLICT(tenant_id,connector_id) DO UPDATE SET run_id=excluded.run_id`, tenant, connector, run)); err != nil {
				return err
			}
		}
		if err = audit.WriteWithin(ctx, q, a.Me.User.ID, "", "admin", action, outcome, "sso_connector", connector, "complete_observation", request); err != nil {
			return err
		}
		var e error
		out, e = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+directoryRunColumns+` FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3`, tenant, connector, run)))
		return e
	})
	if err != nil {
		return directoryRun{}, err
	}
	return out, nil
}
func (d *directoryCandidate) Status(ctx context.Context, a identity.Auth, connector, run string) (directoryRun, error) {
	if !canonicalID(run) {
		return directoryRun{}, fail("VALIDATION_FAILED")
	}
	var out directoryRun
	err := d.within(ctx, a, connector, func(ctx context.Context, q transaction.Executor, tenant string, _ Connector) error {
		var e error
		out, e = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+directoryRunColumns+` FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3 AND actor_user_id=$4 AND actor_session_id=$5 AND actor_epoch=$6`, tenant, connector, run, a.Me.User.ID, a.SessionID, a.Epoch)))
		if errors.Is(e, sql.ErrNoRows) {
			return fail("DIRECTORY_RUN_NOT_FOUND")
		}
		return e
	})
	if err != nil {
		return directoryRun{}, err
	}
	return out, nil
}
func (d *directoryCandidate) Current(ctx context.Context, a identity.Auth, connector string) (directoryCurrent, error) {
	var out directoryCurrent
	err := d.within(ctx, a, connector, func(ctx context.Context, q transaction.Executor, tenant string, c Connector) error {
		if !c.Enabled {
			return fail("DIRECTORY_SOURCE_CHANGED")
		}
		var run string
		var revision, version int64
		var fresh bool
		err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT r.id::text,r.connector_revision,r.auth_version,r.valid_until>clock_timestamp() FROM iam_directory_heads h JOIN iam_directory_runs r ON r.tenant_id=h.tenant_id AND r.connector_id=h.connector_id AND r.id=h.run_id WHERE h.tenant_id=$1 AND h.connector_id=$2 AND r.state='succeeded'`, tenant, connector)).Scan(&run, &revision, &version, &fresh)
		if errors.Is(err, sql.ErrNoRows) {
			return fail("DIRECTORY_GENERATION_UNAVAILABLE")
		}
		if err != nil {
			return err
		}
		if !fresh {
			return fail("DIRECTORY_GENERATION_EXPIRED")
		}
		if c.Revision != revision || c.AuthVersion != version {
			return fail("DIRECTORY_SOURCE_CHANGED")
		}
		out.Run, err = scanDirectoryRun(q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT `+directoryRunColumns+` FROM iam_directory_runs WHERE tenant_id=$1 AND connector_id=$2 AND id=$3`, tenant, connector, run)))
		if err != nil {
			return err
		}
		out.Groups = directoryGroups{}
		rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT external_group_id::text FROM iam_directory_groups WHERE tenant_id=$1 AND connector_id=$2 AND run_id=$3 ORDER BY external_group_id LIMIT $4`, tenant, connector, run, d.MaxGroups+1))
		if err != nil {
			return err
		}
		for rows.Next() {
			var group string
			if err = rows.Scan(&group); err != nil {
				rows.Close()
				return err
			}
			if len(out.Groups) >= d.MaxGroups {
				rows.Close()
				return fail("DIRECTORY_GENERATION_UNAVAILABLE")
			}
			out.Groups[group] = []string{}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		rows, err = q.Query(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT external_group_id::text,external_user_id::text FROM iam_directory_members WHERE tenant_id=$1 AND connector_id=$2 AND run_id=$3 ORDER BY external_group_id,external_user_id LIMIT $4`, tenant, connector, run, d.MaxMemberships+1))
		if err != nil {
			return err
		}
		count := 0
		for rows.Next() {
			var group, user string
			if err = rows.Scan(&group, &user); err != nil {
				rows.Close()
				return err
			}
			count++
			if count > d.MaxMemberships {
				rows.Close()
				return fail("DIRECTORY_GENERATION_UNAVAILABLE")
			}
			out.Groups[group] = append(out.Groups[group], user)
		}
		rows.Close()
		return rows.Err()
	})
	if err != nil {
		return directoryCurrent{}, err
	}
	return out, nil
}
