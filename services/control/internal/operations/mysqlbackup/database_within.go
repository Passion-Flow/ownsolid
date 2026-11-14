package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// readDatabaseWithin computes the post-image using the same actual transaction
// that changed authority. Template is the full strictly verified source inventory;
// only audit_events is omitted to avoid a self-referential audit digest.
func readDatabaseWithin(ctx context.Context, q transaction.Executor, template DatabaseReadback, p config.MySQLBackupConfiguration) (DatabaseReadback, error) {
	if ctx == nil || q == nil || q.Dialect() != transaction.MySQL || p.Validate() != nil {
		return DatabaseReadback{}, errors.New("MYSQL_RESTORE_TRANSACTION_REQUIRED")
	}
	result := DatabaseReadback{Format: "projectone.mysql.database-readback.v1", Tables: []TableReadback{}}
	budget := p.MaximumSQLBytes
	previous := ""
	for _, table := range template.Tables {
		if !identifier.MatchString(table.Table) || table.Table <= previous || len(table.Columns) == 0 || len(table.PrimaryKey) == 0 {
			return DatabaseReadback{}, errors.New("MYSQL_RESTORE_TABLE_TEMPLATE_INVALID")
		}
		previous = table.Table
		columns := map[string]bool{}
		selects := []string{}
		keys := []string{}
		for _, name := range table.Columns {
			if !identifier.MatchString(name) || columns[name] {
				return DatabaseReadback{}, errors.New("MYSQL_RESTORE_TABLE_TEMPLATE_INVALID")
			}
			columns[name] = true
			selects = append(selects, "`"+name+"`")
		}
		seen := map[string]bool{}
		for _, key := range table.PrimaryKey {
			if !columns[key] || seen[key] {
				return DatabaseReadback{}, errors.New("MYSQL_RESTORE_TABLE_TEMPLATE_INVALID")
			}
			seen[key] = true
			keys = append(keys, "`"+key+"`")
		}
		if table.Table == "audit_events" {
			continue
		}
		entry, e := streamTableWithin(ctx, q, table, selects, keys, &budget)
		if e != nil {
			return DatabaseReadback{}, e
		}
		result.Tables = append(result.Tables, entry)
	}
	if len(result.Tables) == 0 {
		return DatabaseReadback{}, errors.New("MYSQL_RESTORE_TABLE_TEMPLATE_INVALID")
	}
	raw, e := json.Marshal(result.Tables)
	if e != nil || int64(len(raw)) > p.MaximumManifestBytes {
		return DatabaseReadback{}, errors.New("MYSQL_BACKUP_MANIFEST_LIMIT_REACHED")
	}
	result.SHA256 = mysqloperator.SHA256(raw)
	return result, nil
}
func streamTableWithin(ctx context.Context, q transaction.Executor, table TableReadback, columns, keys []string, budget *int64) (TableReadback, error) {
	rows, e := q.Query(ctx, transaction.SQL(transaction.MySQL, "SELECT "+strings.Join(columns, ",")+" FROM `"+table.Table+"` ORDER BY "+strings.Join(keys, ",")))
	if e != nil {
		return TableReadback{}, e
	}
	defer rows.Close()
	h := &boundedHash{hash: sha256.New(), remaining: *budget}
	encoder := json.NewEncoder(h)
	if encoder.Encode(table.Columns) != nil {
		return TableReadback{}, errors.New("MYSQL_BACKUP_DATA_LIMIT_REACHED")
	}
	proof := TableReadback{Table: table.Table, Columns: append([]string{}, table.Columns...), PrimaryKey: append([]string{}, table.PrimaryKey...)}
	values := make([]any, len(table.Columns))
	dest := make([]any, len(values))
	for n := range values {
		dest[n] = &values[n]
	}
	for rows.Next() {
		if ctx.Err() != nil {
			return TableReadback{}, ctx.Err()
		}
		if e = rows.Scan(dest...); e != nil {
			return TableReadback{}, e
		}
		if encoder.Encode(values) != nil {
			return TableReadback{}, errors.New("MYSQL_BACKUP_DATA_LIMIT_REACHED")
		}
		proof.Rows++
	}
	if e = rows.Err(); e != nil {
		return TableReadback{}, e
	}
	proof.SHA256 = hex.EncodeToString(h.hash.Sum(nil))
	*budget = h.remaining
	return proof, nil
}
