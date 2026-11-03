package mysqlmigrate

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRuntimeLedgerEntryRefusesPartialChangedAndForeign(t *testing.T) {
	catalog, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	item := catalog.steps[0]
	now := time.Now().UTC()
	valid := sql.NullTime{Time: now.Add(time.Second), Valid: true}
	if e = runtimeLedgerEntry(catalog, 0, 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, now, valid); e != nil {
		t.Fatal(e)
	}
	cases := []struct {
		name                              string
		index, step                       int
		source, object, definition, state string
		started                           time.Time
		applied                           sql.NullTime
	}{
		{"missing-prefix", 0, 2, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, now, valid},
		{"foreign-extra", len(catalog.steps), len(catalog.steps) + 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, now, valid},
		{"changed-source", 0, 1, "changed", item.Object, item.DefinitionSHA256, Applied, now, valid},
		{"foreign-object", 0, 1, item.SourceSHA256, "foreign", item.DefinitionSHA256, Applied, now, valid},
		{"changed-definition", 0, 1, item.SourceSHA256, item.Object, "changed", Applied, now, valid},
		{"uncertain", 0, 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applying, now, valid},
		{"missing-ack", 0, 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, now, sql.NullTime{}},
		{"missing-start", 0, 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, time.Time{}, valid},
		{"reversed-time", 0, 1, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applied, now.Add(time.Minute), valid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if runtimeLedgerEntry(catalog, c.index, c.step, c.source, c.object, c.definition, c.state, c.started, c.applied) == nil {
				t.Fatal("accepted invalid ledger")
			}
		})
	}
}

func TestBorrowedInstallationVerificationRequiresActualSession(t *testing.T) {
	for _, fn := range []func(context.Context, *sql.Conn, Catalog, Options) (Report, error){VerifyRuntimeLedger, VerifyInstallationSession} {
		if r, e := fn(context.Background(), nil, Catalog{}, Options{}); e == nil || r.Ready || r.LockReleased {
			t.Fatal("missing session accepted")
		}
	}
}
