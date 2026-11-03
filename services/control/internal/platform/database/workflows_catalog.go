package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// WorkflowCatalogSQL reads schema-local structure, never customer row contents.
// OIDs and installation schema names are intentionally excluded. The pinned
// PostgreSQL18 catalog supplies exact types/defaults/checks/FKs/indexes/triggers
// and trigger bodies, including validity and enabled state.
const WorkflowCatalogSQL = `WITH owned AS (
 SELECT c.oid,c.relname,c.relkind,c.relrowsecurity,c.relforcerowsecurity
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname IN
 ('workflows','workflow_versions','workflow_conversations','workflow_runs','workflow_node_traces','workflow_mutation_intents')
)
SELECT jsonb_build_object('table_count',(SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),
 'tables',(SELECT jsonb_agg(jsonb_build_array(o.relname,o.relkind,o.relrowsecurity,o.relforcerowsecurity,
  (SELECT jsonb_agg(jsonb_build_array(a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,pg_get_expr(d.adbin,d.adrelid)) ORDER BY a.attnum)
   FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=o.oid AND a.attnum>0 AND NOT a.attisdropped),
  (SELECT jsonb_agg(jsonb_build_array(c.conname,c.contype,c.convalidated,c.condeferrable,c.condeferred,pg_get_constraintdef(c.oid,true)) ORDER BY c.conname) FROM pg_constraint c WHERE c.conrelid=o.oid),
  (SELECT jsonb_agg(jsonb_build_array(c.relname,i.indisunique,i.indisprimary,i.indisvalid,i.indisready,i.indnullsnotdistinct,
    (SELECT jsonb_agg(a.attname ORDER BY k.ordinality) FROM unnest(i.indkey) WITH ORDINALITY k(attnum,ordinality) LEFT JOIN pg_attribute a ON a.attrelid=o.oid AND a.attnum=k.attnum),
    pg_get_expr(i.indpred,i.indrelid),pg_get_expr(i.indexprs,i.indrelid)) ORDER BY c.relname)
   FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=o.oid),
  (SELECT jsonb_agg(jsonb_build_array(t.tgname,t.tgtype,t.tgenabled,p.proname,encode(t.tgargs,'hex'),pg_get_expr(t.tgqual,t.tgrelid),p.prosrc,p.provolatile,p.prosecdef,p.proconfig) ORDER BY t.tgname)
   FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=o.oid AND NOT t.tgisinternal)) ORDER BY o.relname) FROM owned o))::text`

const workflowCatalogFreshSHA256 = "18fffb4d296d5877664bc947e4f536b3dc32af28101f3434c57b2b395c7e7709"

// PostgreSQL18.6 pg_dump/pg_restore reparses enum-array text casts into element
// text casts in six CHECKs and two active-run index predicates. Both complete
// inventories are independently reviewed exact forms of the same constraints.
// No expression, function, type, validity or enabled-state field is ignored.
const workflowCatalogRestoredSHA256 = "103855c7ac7ff9af4c223a4fb6cc5cebbe0975d0a798d7159ed63db1edffa2fd"

func CheckWorkflowsCatalogWithin(ctx context.Context, q transaction.Executor) error {
	if q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("WORKFLOW_CATALOG_INCOMPATIBLE")
	}
	var raw string
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, WorkflowCatalogSQL)).Scan(&raw); e != nil {
		return errors.New("WORKFLOW_CATALOG_INCOMPATIBLE")
	}
	digest := sha256.Sum256([]byte(raw))
	switch hex.EncodeToString(digest[:]) {
	case workflowCatalogFreshSHA256, workflowCatalogRestoredSHA256:
		return nil
	default:
		return errors.New("WORKFLOW_CATALOG_INCOMPATIBLE")
	}
}
