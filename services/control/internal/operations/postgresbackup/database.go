package postgresbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type DatabaseReadback struct {
	TenantID      string `json:"tenant_id"`
	Database      string `json:"database"`
	Operator      string `json:"operator"`
	BackendPID    int    `json:"backend_pid"`
	SchemaVersion int    `json:"schema_version"`
	Tables        int    `json:"tables"`
	Sequences     int    `json:"sequences"`
	Rows          int64  `json:"rows"`
	ContentSHA256 string `json:"content_sha256"`
}

// Each observation starts a new read-only transaction on the SAME borrowed
// installation-lock session. A later observation sees intervening changes;
// reusing one repeatable-read snapshot would hide those changes.
func readDatabase(ctx context.Context, lease *pgxpool.Conn, r release.PostgreSQLBackupRequest) (DatabaseReadback, error) {
	return withDatabaseSnapshot(ctx, lease, r, nil)
}

var snapshotPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{8}-[0-9]+$`)

func withDatabaseSnapshot(ctx context.Context, lease *pgxpool.Conn, r release.PostgreSQLBackupRequest, consume func(pgx.Tx, DatabaseReadback) error) (out DatabaseReadback, result error) {
	if ctx == nil || lease == nil || !r.Valid() {
		return out, errors.New("POSTGRES_BACKUP_LOCKED_CONTEXT_REQUIRED")
	}
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_READ_ONLY_TRANSACTION_UNAVAILABLE")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(r.Policy.TerminationGraceSeconds))
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			result = errors.Join(result, errors.New("POSTGRES_BACKUP_TRANSACTION_CLOSE_UNCONFIRMED"))
			out = DatabaseReadback{}
		}
	}()
	out, e = readDatabaseWithin(ctx, tx, r)
	if e != nil {
		return DatabaseReadback{}, e
	}
	if consume != nil {
		if e = consume(tx, out); e != nil {
			return DatabaseReadback{}, e
		}
	}
	if _, e = postgresmaintenance.CheckWithin(ctx, transaction.PostgreSQLTransaction(tx)); e != nil {
		return DatabaseReadback{}, e
	}
	return out, nil
}

func readDatabaseWithin(ctx context.Context, tx pgx.Tx, r release.PostgreSQLBackupRequest) (out DatabaseReadback, result error) {
	observed, e := postgresmaintenance.CheckWithin(ctx, transaction.PostgreSQLTransaction(tx))
	if e != nil {
		return out, e
	}
	if observed.TenantID != r.TenantID || observed.Database != r.Database || observed.Operator != r.Operator {
		return out, errors.New("POSTGRES_BACKUP_INSTALLATION_BINDING_MISMATCH")
	}
	var version string
	if tx.QueryRow(ctx, "SELECT pg_catalog.current_setting('server_version_num')").Scan(&version) != nil || version != "180006" {
		return out, errors.New("POSTGRES_BACKUP_SERVER_VERSION_MISMATCH")
	}
	data, e := readProfileDatabaseContents(ctx, tx, r.Policy, "", r.EffectiveApplicationProfile())
	if e != nil {
		return out, e
	}
	out = data
	if _, e = postgresmaintenance.CheckWithin(ctx, transaction.PostgreSQLTransaction(tx)); e != nil {
		return DatabaseReadback{}, e
	}
	out.TenantID = observed.TenantID
	out.Database = observed.Database
	out.Operator = observed.Operator
	out.BackendPID = observed.BackendPID
	out.SchemaVersion = observed.SchemaVersion
	return out, nil
}

// The complete source gate remains in readDatabaseWithin. Restore borrows this
// content reader only after checking its own exact held target/catalog. The
// one explicitly bound restore audit is checked separately, avoiding a circular
// digest that would contain its own post-image hash.
func readDatabaseContents(ctx context.Context, tx pgx.Tx, p release.PostgreSQLBackupPolicy, excludedRestoreAudit string) (DatabaseReadback, error) {
	return readProfileDatabaseContents(ctx, tx, p, excludedRestoreAudit, database.LineageName)
}

func readProfileDatabaseContents(ctx context.Context, tx pgx.Tx, p release.PostgreSQLBackupPolicy, excludedRestoreAudit, profile string) (out DatabaseReadback, result error) {
	if ctx == nil || tx == nil || !p.Valid() {
		return out, errors.New("POSTGRES_RESTORE_CONTENT_CONTEXT_INVALID")
	}
	if excludedRestoreAudit != "" {
		id, e := uuid.Parse(excludedRestoreAudit)
		if e != nil || id.String() != excludedRestoreAudit {
			return out, errors.New("POSTGRES_RESTORE_AUDIT_ID_INVALID")
		}
	}
	spec, e := selectedBackupSchema(profile)
	if e != nil {
		return out, e
	}
	if e := database.CheckWithin(ctx, transaction.PostgreSQLTransaction(tx)); e != nil {
		return out, errors.New("POSTGRES_RESTORE_CONTENT_SCHEMA_INVALID")
	}
	tables, e := tx.Query(ctx, `SELECT c.relname FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY c.relname COLLATE "C"`)
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
	}
	names := []string{}
	for tables.Next() {
		var n string
		if tables.Scan(&n) != nil {
			tables.Close()
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
		names = append(names, n)
		if len(names) > spec.tables {
			tables.Close()
			return out, errors.New("POSTGRES_BACKUP_CATALOG_CHANGED")
		}
	}
	e = tables.Err()
	tables.Close()
	if e != nil || len(names) != spec.tables {
		return out, errors.New("POSTGRES_BACKUP_CATALOG_CHANGED")
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	var contentBytes int64
	for _, name := range names {
		if encoder.Encode([]string{"table", name}) != nil {
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
		// Hash each row on PostgreSQL, then stream only its fixed-size SHA256
		// and original byte count. No complete customer row is allocated in Go.
		where := ""
		args := []any{}
		source := pgx.Identifier{"public", name}.Sanitize()
		if name == "audit_events" && excludedRestoreAudit != "" {
			where = " WHERE t.id<>$1::uuid"
			args = append(args, excludedRestoreAudit)
		}
		if name == "audit_chain_head" && excludedRestoreAudit != "" {
			// The restore receipt itself is excluded from this comparison. Its
			// predecessor is the corresponding snapshot of the durable head.
			var position int64
			var predecessor []byte
			var isHead bool
			err := tx.QueryRow(ctx, `SELECT e.chain_position-1,e.prev_hash,e.chain_position=h.chain_position AND e.hash=h.hash
				FROM public.audit_events e CROSS JOIN public.audit_chain_head h WHERE e.id=$1::uuid AND h.singleton`, excludedRestoreAudit).
				Scan(&position, &predecessor, &isHead)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				// The pre-commit comparison runs before the receipt exists.
			case err != nil || !isHead:
				return out, errors.New("POSTGRES_RESTORE_AUDIT_HEAD_MISMATCH")
			default:
				source = `(SELECT singleton,$1::bigint AS chain_position,$2::bytea AS hash FROM public.audit_chain_head)`
				args = append(args, position, predecessor)
			}
		}
		query := `SELECT measured.row_sha256,measured.row_bytes FROM (SELECT pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(pg_catalog.to_jsonb(t)::text,'UTF8')),'hex') AS row_sha256,pg_catalog.octet_length(pg_catalog.to_jsonb(t)::text)::bigint AS row_bytes FROM ` + source + ` t` + where + `) AS measured ORDER BY measured.row_sha256 COLLATE "C"`
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
		for rows.Next() {
			var raw string
			var size int64
			if rows.Scan(&raw, &size) != nil || !release.ValidSHA256(raw) || size < 0 {
				rows.Close()
				return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
			}
			if size > p.MaximumSQLBytes-contentBytes {
				rows.Close()
				return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_LIMIT_REACHED")
			}
			contentBytes += size
			if encoder.Encode([]string{raw, strconv.FormatInt(size, 10)}) != nil {
				rows.Close()
				return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
			}
			out.Rows++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
	}
	sequences, e := tx.Query(ctx, `SELECT sequencename FROM pg_catalog.pg_sequences WHERE schemaname='public' ORDER BY sequencename COLLATE "C"`)
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
	}
	seqs := []string{}
	for sequences.Next() {
		var n string
		if sequences.Scan(&n) != nil {
			sequences.Close()
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
		seqs = append(seqs, n)
		if len(seqs) > spec.tables {
			sequences.Close()
			return out, errors.New("POSTGRES_BACKUP_CATALOG_CHANGED")
		}
	}
	e = sequences.Err()
	sequences.Close()
	if e != nil {
		return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
	}
	for _, name := range seqs {
		var value int64
		var called bool
		if tx.QueryRow(ctx, `SELECT last_value,is_called FROM `+pgx.Identifier{"public", name}.Sanitize()).Scan(&value, &called) != nil {
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
		if encoder.Encode([]string{"sequence", name, strconv.FormatInt(value, 10), strconv.FormatBool(called)}) != nil {
			return out, errors.New("POSTGRES_BACKUP_DATABASE_READ_FAILED")
		}
	}
	out.SchemaVersion = spec.version
	out.Tables = len(names)
	out.Sequences = len(seqs)
	out.ContentSHA256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
