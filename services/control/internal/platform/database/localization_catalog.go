package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// LocalizationCatalogSQL reads schema-local structure, never customer row contents.
// OIDs and installation schema names are intentionally excluded. The pinned
// PostgreSQL18 catalog supplies exact types/defaults/checks/FKs/indexes/triggers
// and trigger bodies, including validity and enabled state.
const LocalizationCatalogSQL = `WITH owned AS (
 SELECT c.oid,c.relname,c.relkind,c.relrowsecurity,c.relforcerowsecurity
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relname IN
 ('users','invitations','mail_deliveries','user_preferences','terminal_notifications','workflows','workflow_versions','workflow_conversations','workflow_runs','workflow_node_traces','workflow_mutation_intents','plugin_versions','plugin_grants','plugin_mutation_intents','workflow_plugin_references')
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

const localizationCatalogFreshSHA256 = "3930a5ef339d44f3b5672c12473dfa6e455b8fe87820057fbbd4a871a17462da"

// PostgreSQL18.6 pg_dump/pg_restore reparses enum-array text casts into element
// text casts in user/preference/notice/plugin/workflow CHECKs and active-run index predicates. Both complete
// inventories are independently reviewed exact forms of the same constraints.
// No expression, function, type, validity or enabled-state field is ignored.
const localizationCatalogRestoredSHA256 = "11d391c06c3e8ddec75a39c659f4a01ff7aea1551c62af39125bc8e3653e9b3c"

func CheckLocalizationCatalogWithin(ctx context.Context, q transaction.Executor) error {
	if q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("LOCALIZATION_CATALOG_INCOMPATIBLE")
	}
	var raw string
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, LocalizationCatalogSQL)).Scan(&raw); e != nil {
		return errors.New("LOCALIZATION_CATALOG_INCOMPATIBLE")
	}
	digest := sha256.Sum256([]byte(raw))
	switch hex.EncodeToString(digest[:]) {
	case localizationCatalogFreshSHA256, localizationCatalogRestoredSHA256:
		return nil
	default:
		return errors.New("LOCALIZATION_CATALOG_INCOMPATIBLE")
	}
}
