package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CatalogSQL binds every schema-local table/column/default,
// constraint/index/trigger and function (including enabled/validity/security
// attributes). OIDs, data and installation schema names are not fingerprints, and neither are
// partitions: they come and go at run time (ADR 0029), and a partition has its parent's shape.
const CatalogSQL = `WITH owned AS (
 SELECT c.oid,c.relname,c.relkind,c.relrowsecurity,c.relforcerowsecurity,c.relpersistence,c.relreplident
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=current_schema() AND c.relkind IN ('r','p','v','m','f','S') AND NOT c.relispartition
)
SELECT jsonb_build_object('table_count',(SELECT count(*) FROM owned WHERE relkind IN ('r','p')),
 'functions',(SELECT jsonb_agg(jsonb_build_array(p.proname,pg_get_function_identity_arguments(p.oid),p.prosrc,p.provolatile,p.proisstrict,p.prosecdef,p.proparallel,p.proconfig,p.prokind,pg_get_function_result(p.oid),(SELECT lanname FROM pg_language l WHERE l.oid=p.prolang),p.proleakproof,p.proargnames,p.proargmodes) ORDER BY p.proname,pg_get_function_identity_arguments(p.oid)) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND NOT EXISTS(SELECT 1 FROM pg_depend dep WHERE dep.classid='pg_proc'::regclass AND dep.objid=p.oid AND dep.deptype='e')),
 'tables',(SELECT jsonb_agg(jsonb_build_array(o.relname,o.relkind,o.relrowsecurity,o.relforcerowsecurity,o.relpersistence,o.relreplident,
  (SELECT jsonb_agg(jsonb_build_array(a.attname,replace(format_type(a.atttypid,a.atttypmod),'public.vector','vector'),a.attnotnull,a.attidentity,a.attgenerated,(SELECT co.collname FROM pg_collation co WHERE co.oid=a.attcollation),a.attstorage,a.attcompression,pg_get_expr(d.adbin,d.adrelid)) ORDER BY a.attnum)
   FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=o.oid AND a.attnum>0 AND NOT a.attisdropped),
  (SELECT jsonb_agg(jsonb_build_array(c.conname,c.contype,c.convalidated,c.condeferrable,c.condeferred,replace(pg_get_constraintdef(c.oid,true),'public.vector','vector')) ORDER BY c.conname) FROM pg_constraint c WHERE c.conrelid=o.oid),
  (SELECT jsonb_agg(jsonb_build_array(c.relname,(SELECT amname FROM pg_am am WHERE am.oid=c.relam),i.indisunique,i.indisprimary,i.indisvalid,i.indisready,i.indnullsnotdistinct,i.indnkeyatts,i.indoption::text,c.reloptions,
    (SELECT jsonb_agg(op.opcname ORDER BY k.ordinality) FROM unnest(i.indclass) WITH ORDINALITY k(opc,ordinality) JOIN pg_opclass op ON op.oid=k.opc),
    (SELECT jsonb_agg(co.collname ORDER BY k.ordinality) FROM unnest(i.indcollation) WITH ORDINALITY k(coll,ordinality) LEFT JOIN pg_collation co ON co.oid=k.coll),
    (SELECT jsonb_agg(a.attname ORDER BY k.ordinality) FROM unnest(i.indkey) WITH ORDINALITY k(attnum,ordinality) LEFT JOIN pg_attribute a ON a.attrelid=o.oid AND a.attnum=k.attnum),
    pg_get_expr(i.indpred,i.indrelid),pg_get_expr(i.indexprs,i.indrelid)) ORDER BY c.relname)
   FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=o.oid),
  (SELECT jsonb_agg(jsonb_build_array(t.tgname,t.tgtype,t.tgenabled,p.proname,encode(t.tgargs,'hex'),pg_get_expr(t.tgqual,t.tgrelid),p.prosrc,p.provolatile,p.prosecdef,p.proconfig) ORDER BY t.tgname)
   FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid WHERE t.tgrelid=o.oid AND NOT t.tgisinternal)) ORDER BY o.relname) FROM owned o))::text`

// Exact native fresh and logical dump/reparse forms from pinned PostgreSQL18.6.
const catalogFreshSHA256 = "0fecddab3f8f63728c7f714230b718690821463a52f6536310128d60799d5e29"
const catalogRestoredSHA256 = "cc33629b40a9bb5f42edd1fadc40ca6f29f04e553e378ebd47b3813b47ef62a1"

func checkCatalogWithin(ctx context.Context, q transaction.Executor) error {
	var raw string
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, CatalogSQL)).Scan(&raw); err != nil {
		return queryFailed(err, "SCHEMA_CATALOG_INCOMPATIBLE")
	}
	digest := sha256.Sum256([]byte(raw))
	switch hex.EncodeToString(digest[:]) {
	case catalogFreshSHA256, catalogRestoredSHA256:
	default:
		return errors.New("SCHEMA_CATALOG_INCOMPATIBLE")
	}
	return checkLocalCellWithin(ctx, q)
}

// checkLocalCellWithin refuses a database that serves a cell other than the one this
// release can run. Multi-cell installs replace the constant with configuration (ARC-202).
func checkLocalCellWithin(ctx context.Context, q transaction.Executor) error {
	var site, cell string
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT site_id,id FROM cells WHERE is_local")).Scan(&site, &cell); err != nil {
		return queryFailed(err, "SCHEMA_CELL_IDENTITY_INVALID")
	}
	if site != wire.DefaultSiteID || cell != wire.DefaultCellID {
		return errors.New("SCHEMA_CELL_IDENTITY_INVALID")
	}
	return nil
}
