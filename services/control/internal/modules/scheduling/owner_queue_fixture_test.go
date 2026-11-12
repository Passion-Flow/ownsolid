package scheduling

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// The persistent queue is a disposable owner model, not the training schema.
// Read and write admission fixtures share its tenant/local scope and revisions.
func createOwnerQueueEntries(t *testing.T, f reservationFixture, entries []QueueEntry) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `CREATE TABLE owner_fixture_queue (
 job_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 site_id varchar NOT NULL DEFAULT local_site_id(),
 cell_id varchar NOT NULL DEFAULT local_cell_id(),
 priority integer NOT NULL,
 sequence bigint NOT NULL UNIQUE CHECK(sequence > 0),
 state text NOT NULL DEFAULT 'queued',
 revision bigint NOT NULL DEFAULT 1,
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
)`); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO owner_fixture_queue
 (job_id,tenant_id,workspace_id,priority,sequence) VALUES($1,$2,$3,$4,$5)`,
			entry.JobID, f.first.TenantID, entry.QueueID, entry.Priority, entry.Sequence); err != nil {
			t.Fatal(err)
		}
	}
}

func readOwnerQueueEntries(ctx context.Context, q transaction.Executor, tenant string) ([]QueueEntry, error) {
	rows, err := q.Query(ctx, transaction.SQL(transaction.PostgreSQL,
		`SELECT job_id::text,workspace_id::text,priority,sequence FROM owner_fixture_queue
 WHERE tenant_id=$1 AND site_id=local_site_id() AND cell_id=local_cell_id()
 AND state='queued' ORDER BY sequence`, tenant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []QueueEntry
	for rows.Next() {
		var entry QueueEntry
		if err := rows.Scan(&entry.JobID, &entry.QueueID, &entry.Priority, &entry.Sequence); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
