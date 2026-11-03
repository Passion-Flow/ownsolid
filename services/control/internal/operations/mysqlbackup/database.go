// Package mysqlbackup owns explicit native MySQL logical backup and isolated
// restore. It borrows actual selected installation/session and module owners.
package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"regexp"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type TableReadback struct {
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	PrimaryKey []string `json:"primary_key"`
	Rows       int64    `json:"rows"`
	SHA256     string   `json:"sha256"`
}
type DatabaseReadback struct {
	Format string          `json:"format"`
	Tables []TableReadback `json:"tables"`
	SHA256 string          `json:"sha256"`
}

type boundedHash struct {
	hash      hash.Hash
	remaining int64
}

func (h *boundedHash) Write(p []byte) (int, error) {
	if int64(len(p)) > h.remaining {
		return 0, errors.New("MYSQL_BACKUP_DATA_LIMIT_REACHED")
	}
	n, e := h.hash.Write(p)
	h.remaining -= int64(n)
	return n, e
}

func tableNames(ctx context.Context, c *sql.Conn) ([]string, error) {
	rows, e := c.QueryContext(ctx, `SELECT TABLE_NAME,ENGINE,TABLE_TYPE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME`)
	if e != nil {
		return nil, errors.New("MYSQL_BACKUP_TABLE_INVENTORY_UNAVAILABLE")
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name, engine, kind string
		if rows.Scan(&name, &engine, &kind) != nil || !identifier.MatchString(name) || engine != "InnoDB" || kind != "BASE TABLE" {
			return nil, errors.New("MYSQL_BACKUP_INNODB_TABLES_REQUIRED")
		}
		names = append(names, name)
	}
	if rows.Err() != nil || len(names) == 0 {
		return nil, errors.New("MYSQL_BACKUP_TABLE_INVENTORY_UNAVAILABLE")
	}
	return names, nil
}

func primaryColumns(ctx context.Context, c *sql.Conn, table string) ([]string, error) {
	rows, e := c.QueryContext(ctx, `SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY' ORDER BY ORDINAL_POSITION`, table)
	if e != nil {
		return nil, errors.New("MYSQL_BACKUP_PRIMARY_KEY_REQUIRED")
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var column string
		if rows.Scan(&column) != nil || !identifier.MatchString(column) {
			return nil, errors.New("MYSQL_BACKUP_PRIMARY_KEY_REQUIRED")
		}
		columns = append(columns, column)
	}
	if rows.Err() != nil || len(columns) == 0 {
		return nil, errors.New("MYSQL_BACKUP_PRIMARY_KEY_REQUIRED")
	}
	return columns, nil
}

func tableReadback(ctx context.Context, c *sql.Conn, name string, budget *int64) (TableReadback, error) {
	keys, e := primaryColumns(ctx, c, name)
	if e != nil {
		return TableReadback{}, e
	}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = "`" + key + "`"
	}
	rows, e := c.QueryContext(ctx, "SELECT * FROM `"+name+"` ORDER BY "+strings.Join(quoted, ","))
	if e != nil {
		return TableReadback{}, errors.New("MYSQL_BACKUP_ROW_READBACK_FAILED")
	}
	defer rows.Close()
	columns, e := rows.Columns()
	if e != nil || len(columns) == 0 {
		return TableReadback{}, errors.New("MYSQL_BACKUP_ROW_READBACK_FAILED")
	}
	for _, column := range columns {
		if !identifier.MatchString(column) {
			return TableReadback{}, errors.New("MYSQL_BACKUP_COLUMN_INVALID")
		}
	}
	h := &boundedHash{hash: sha256.New(), remaining: *budget}
	encoder := json.NewEncoder(h)
	if encoder.Encode(columns) != nil {
		return TableReadback{}, errors.New("MYSQL_BACKUP_DATA_LIMIT_REACHED")
	}
	proof := TableReadback{Table: name, Columns: columns, PrimaryKey: keys}
	values := make([]any, len(columns))
	destinations := make([]any, len(columns))
	for i := range values {
		destinations[i] = &values[i]
	}
	for rows.Next() {
		if ctx.Err() != nil {
			return TableReadback{}, ctx.Err()
		}
		if rows.Scan(destinations...) != nil {
			return TableReadback{}, errors.New("MYSQL_BACKUP_ROW_READBACK_FAILED")
		}
		// Encode this one row before Next can reuse driver buffers. No all-row or
		// all-table value accumulation: retained results contain only digests.
		if encoder.Encode(values) != nil {
			return TableReadback{}, errors.New("MYSQL_BACKUP_DATA_LIMIT_REACHED")
		}
		proof.Rows++
	}
	if rows.Err() != nil {
		return TableReadback{}, errors.New("MYSQL_BACKUP_ROW_READBACK_FAILED")
	}
	if e = rows.Close(); e != nil {
		return TableReadback{}, errors.New("MYSQL_BACKUP_ROW_READBACK_FAILED")
	}
	proof.SHA256 = hex.EncodeToString(h.hash.Sum(nil))
	*budget = h.remaining
	return proof, nil
}

// ReadDatabase streams a deterministic full image on the actual locked session.
// The caller keeps the stopped-runtime/installation lease throughout its native
// dump or restore and repeats this proof; it is not an online writer exclusion.
func ReadDatabase(ctx context.Context, o *mysqloperator.Context, policy config.MySQLBackupConfiguration, omitAudit bool) (DatabaseReadback, error) {
	if policy.Validate() != nil {
		return DatabaseReadback{}, errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED")
	}
	c, e := o.Lease(ctx)
	if e != nil {
		return DatabaseReadback{}, e
	}
	if e = o.VerifyInstallation(ctx); e != nil {
		return DatabaseReadback{}, e
	}
	names, e := tableNames(ctx, c)
	if e != nil {
		return DatabaseReadback{}, e
	}
	result := DatabaseReadback{Format: "projectone.mysql.database-readback.v1", Tables: []TableReadback{}}
	budget := policy.MaximumSQLBytes
	for _, name := range names {
		if omitAudit && name == "audit_events" {
			continue
		}
		entry, e := tableReadback(ctx, c, name, &budget)
		if e != nil {
			return DatabaseReadback{}, e
		}
		result.Tables = append(result.Tables, entry)
	}
	raw, e := json.Marshal(result.Tables)
	if e != nil || int64(len(raw)) > policy.MaximumManifestBytes {
		return DatabaseReadback{}, errors.New("MYSQL_BACKUP_MANIFEST_LIMIT_REACHED")
	}
	result.SHA256 = mysqloperator.SHA256(raw)
	if e = o.Verify(ctx); e != nil {
		return DatabaseReadback{}, e
	}
	return result, nil
}

func hashStream(ctx context.Context, destination io.Writer, source io.Reader, maximum int64) (string, int64, error) {
	if ctx == nil || maximum < 1 {
		return "", 0, errors.New("MYSQL_BACKUP_STREAM_INVALID")
	}
	h := sha256.New()
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return "", 0, e
		}
		n, readError := source.Read(buffer)
		if n > 0 {
			if int64(n) > maximum-total {
				return "", 0, errors.New("MYSQL_BACKUP_STREAM_LIMIT_REACHED")
			}
			if _, e := h.Write(buffer[:n]); e != nil {
				return "", 0, e
			}
			if destination != nil {
				written, e := destination.Write(buffer[:n])
				if e != nil || written != n {
					return "", 0, errors.New("MYSQL_BACKUP_STREAM_WRITE_FAILED")
				}
			}
			total += int64(n)
		}
		if readError == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), total, nil
		}
		if readError != nil {
			return "", 0, errors.New("MYSQL_BACKUP_STREAM_READ_FAILED")
		}
	}
}
