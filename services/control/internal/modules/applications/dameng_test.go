package applications

import (
	"reflect"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
)

func TestDamengApplicationProjectionBinding(t *testing.T) {
	at := time.Date(2026, 10, 24, 7, 0, 0, 123456000, time.UTC)
	id := "11111111-1111-4111-8111-111111111111"
	for _, conversation := range []bool{false, true} {
		for _, next := range []bool{false, true} {
			cursor := pagination.Cursor{}
			args := []any{"tenant", "workspace"}
			query := `SELECT f.id,f.workspace_id,f.name,v.id,v.version_number,v.graph_sha256,v.published_at FROM workflows f JOIN workflow_versions v ON v.tenant_id=f.tenant_id AND v.workspace_id=f.workspace_id AND v.workflow_id=f.id AND v.id=f.current_version_id WHERE f.tenant_id=? AND f.workspace_id=? AND f.enabled=1`
			stamp, key := "v.published_at", "f.id"
			if conversation {
				query = `SELECT c.id,c.workspace_id,c.workflow_id,f.name,c.version_id,v.version_number,c.turn_count,c.created_at,c.updated_at,f.enabled FROM workflow_conversations c JOIN workflows f ON f.tenant_id=c.tenant_id AND f.workspace_id=c.workspace_id AND f.id=c.workflow_id JOIN workflow_versions v ON v.tenant_id=c.tenant_id AND v.workspace_id=c.workspace_id AND v.workflow_id=c.workflow_id AND v.id=c.version_id WHERE c.tenant_id=? AND c.workspace_id=? AND c.owner_id=? AND c.deleted_at IS NULL`
				stamp, key = "c.updated_at", "c.id"
				args = append(args, "actor")
			}
			if next {
				// Normalizing a representable offset changes no instant or ordering.
				cursor = pagination.Cursor{Time: at.In(time.FixedZone("offset", 3600)), ID: id}
				query += " AND (" + stamp + "<? OR (" + stamp + "=? AND " + key + "<?))"
				args = append(args, at, at, id)
			}
			query += " ORDER BY " + stamp + " DESC," + key + " DESC LIMIT 8"
			got, err := projectionQuery(transaction.Dameng, conversation, "tenant", "workspace", "actor", cursor, 7)
			if err != nil || !reflect.DeepEqual(got, transaction.SQL(transaction.Dameng, query, args...)) {
				t.Fatal("native scope, publication, private owner or cursor binding changed", err)
			}
		}
	}
}

func TestDamengApplicationUnrepresentableCursorAndLimit(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for _, at := range []time.Time{
		{}, time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 24, 7, 0, 0, 123456001, time.UTC),
	} {
		_, err := projectionQuery(transaction.Dameng, false, "tenant", "workspace", "actor", pagination.Cursor{Time: at, ID: id}, 7)
		if err == nil || err.Error() != "INVALID_CURSOR" {
			t.Fatal("unrepresentable native cursor accepted", err)
		}
	}
	for _, n := range []int{-1, 0, pagination.MaximumLimit + 1} {
		if _, err := projectionQuery(transaction.Dameng, false, "tenant", "workspace", "actor", pagination.Cursor{}, n); err == nil {
			t.Fatal("unbounded LIMIT accepted")
		}
	}
	if _, err := projectionQuery(transaction.Dialect("unknown"), false, "tenant", "workspace", "actor", pagination.Cursor{}, 7); err == nil {
		t.Fatal("unknown vendor fell back to PostgreSQL")
	}
	for _, at := range []time.Time{time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)} {
		if _, err := projectionQuery(transaction.Dameng, true, "tenant", "workspace", "actor", pagination.Cursor{Time: at, ID: id}, pagination.MaximumLimit); err != nil {
			t.Fatal("representable native cursor rejected", err)
		}
	}
}
