//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"strconv"
)

// Called only after complete table/key/column/delete-rule readback. DM reserves
// REF_CON_TRIG/SREF_CON_TRIG names against user CREATE TRIGGER (native manual).
// A prefix alone grants nothing: each allowed object is derived from an exact
// SYSCONS foreign-key ID, table/reference-index identity and action projection.
func (n *nativeBackend) internalFKTriggers(ctx context.Context, tables map[string]table, objects map[string]string) (map[string]bool, error) {
	allowed := map[string]bool{}
	for _, table := range tables {
		for _, key := range table.Constraints {
			if key.Kind != "R" {
				continue
			}
			var id, trigid int64
			var action string
			e := n.connection.QueryRowContext(ctx, `SELECT c.ID,c.TRIGID,c.FACTION FROM SYS.SYSCONS c JOIN SYS.SYSOBJECTS co ON co.ID=c.ID AND co.TYPE$='TABOBJ' AND co.SUBTYPE$='CONS' JOIN SYS.SYSOBJECTS t ON t.ID=c.TABLEID AND t.SUBTYPE$='UTAB' AND co.SCHID=t.SCHID JOIN SYS.SYSOBJECTS s ON s.ID=t.SCHID AND s.TYPE$='SCH' JOIN SYS.SYSCONS rc ON rc.INDEXID=c.FINDEXID AND rc.TYPE$ IN ('P','U') JOIN SYS.SYSOBJECTS rco ON rco.ID=rc.ID AND rco.SCHID=s.ID JOIN SYS.SYSOBJECTS rt ON rt.ID=rc.TABLEID AND rt.SCHID=s.ID WHERE s.NAME=? AND t.NAME=? AND co.NAME=? AND c.TYPE$='F' AND c.VALID='Y' AND rco.NAME=? AND rt.NAME=?`, n.configuration.Schema, table.Name, key.Name, key.ReferenceKey, key.Reference).Scan(&id, &trigid, &action)
			if e != nil || id <= 0 {
				return nil, fail("DAMENG_SCHEMA_FK_SYSTEM_METADATA_UNAVAILABLE")
			}
			want := "AA"
			if n.catalog.deleteRules[key.Name] == "CASCADE" {
				want = "AC"
			}
			if action != want {
				return nil, fail("DAMENG_SCHEMA_FK_ACTION_DRIFT")
			}
			names := []string{}
			switch {
			case trigid > 0:
				if want != "AC" {
					return nil, fail("DAMENG_SCHEMA_FK_TRIGGER_DRIFT")
				}
				var name string
				if n.connection.QueryRowContext(ctx, "SELECT g.NAME FROM SYS.SYSOBJECTS g JOIN SYS.SYSOBJECTS s ON s.ID=g.SCHID WHERE g.ID=? AND g.TYPE$='SCHOBJ' AND g.SUBTYPE$='TRIG' AND s.NAME=?", trigid, n.configuration.Schema).Scan(&name) != nil || name != "REF_CON_TRIG"+strconv.FormatInt(id, 10) {
					return nil, fail("DAMENG_SCHEMA_FK_TRIGGER_DRIFT")
				}
				names = append(names, name)
			case trigid == -1 && table.Name == key.Reference:
				// Exact ID-derived self-reference triplet, not a wildcard allowance.
				for _, suffix := range []string{"_AS", "_BS", "_AR"} {
					names = append(names, "SREF_CON_TRIG"+strconv.FormatInt(id, 10)+suffix)
				}
			case trigid == 0 && want == "AA":
			default:
				return nil, fail("DAMENG_SCHEMA_FK_TRIGGER_DRIFT")
			}
			for _, name := range names {
				if objects[name] != "TRIGGER" || allowed[name] {
					return nil, fail("DAMENG_SCHEMA_FK_TRIGGER_DRIFT")
				}
				var actual, status string
				var objectID int64
				if n.connection.QueryRowContext(ctx, "SELECT tr.TABLE_NAME,tr.STATUS,g.ID FROM ALL_TRIGGERS tr JOIN SYS.SYSOBJECTS s ON s.NAME=tr.OWNER AND s.TYPE$='SCH' JOIN SYS.SYSOBJECTS g ON g.NAME=tr.TRIGGER_NAME AND g.SCHID=s.ID AND g.TYPE$='SCHOBJ' AND g.SUBTYPE$='TRIG' WHERE tr.OWNER=? AND tr.TRIGGER_NAME=?", n.configuration.Schema, name).Scan(&actual, &status, &objectID) != nil || objectID <= 0 || actual != key.Reference || status != "ENABLED" || trigid > 0 && objectID != trigid {
					return nil, fail("DAMENG_SCHEMA_FK_TRIGGER_DRIFT")
				}
				allowed[name] = true
			}
		}
	}
	return allowed, nil
}
