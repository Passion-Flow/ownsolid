package database

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// ApprovedKnowledgeVectorSQL verifies only catalog metadata, never installs or repairs.
// Objects and C symbols follow the pinned pgvector0.8.7 sql/vector.sql; extension
// membership, public schema, privileged ownership and exact server version are required.
const ApprovedKnowledgeVectorSQL = `WITH extension AS (
 SELECT e.oid,e.extowner,e.extnamespace FROM pg_catalog.pg_extension e
 JOIN pg_catalog.pg_namespace n ON n.oid=e.extnamespace
 JOIN pg_catalog.pg_roles r ON r.oid=e.extowner
 WHERE e.extname='vector' AND e.extversion='0.8.7' AND n.nspname='public' AND r.rolsuper
), vector_type AS (
 SELECT t.* FROM pg_catalog.pg_type t JOIN extension e ON t.typnamespace=e.extnamespace AND t.typowner=e.extowner
 JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_type'::regclass AND d.objid=t.oid AND d.refobjid=e.oid AND d.deptype='e'
 WHERE t.typname='vector' AND t.typtype='b' AND t.typisdefined
), functions AS (
 SELECT p.* FROM pg_catalog.pg_proc p JOIN extension e ON p.pronamespace=e.extnamespace AND p.proowner=e.extowner
 JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_proc'::regclass AND d.objid=p.oid AND d.refobjid=e.oid AND d.deptype='e'
 JOIN pg_catalog.pg_language l ON l.oid=p.prolang
 WHERE l.lanname='c' AND p.probin='$libdir/vector' AND p.prosrc=p.proname
 AND p.provolatile='i' AND p.proisstrict AND p.proparallel='s' AND NOT p.prosecdef AND p.proconfig IS NULL
)
SELECT pg_catalog.current_setting('server_version_num')::integer=180006
 AND (SELECT count(*) FROM extension)=1 AND (SELECT count(*) FROM vector_type)=1
 AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE n.nspname='public' AND a.grantee=0 AND a.privilege_type='CREATE')
 AND (SELECT count(*) FROM vector_type t JOIN functions p ON p.oid IN (t.typinput,t.typoutput,t.typreceive,t.typsend,t.typmodin))=5
 AND (SELECT count(*) FROM functions p CROSS JOIN vector_type t WHERE (p.proname IN ('vector_norm','vector_dims') AND p.pronargs=1 AND p.proargtypes[0]=t.oid) OR (p.proname='cosine_distance' AND p.pronargs=2 AND p.proargtypes[0]=t.oid AND p.proargtypes[1]=t.oid))=3
 AND EXISTS (SELECT 1 FROM pg_catalog.pg_operator o JOIN extension e ON o.oprnamespace=e.extnamespace AND o.oprowner=e.extowner JOIN vector_type t ON o.oprleft=t.oid AND o.oprright=t.oid JOIN functions p ON p.oid=o.oprcode AND p.proname='cosine_distance' JOIN pg_catalog.pg_depend d ON d.classid='pg_catalog.pg_operator'::regclass AND d.objid=o.oid AND d.refobjid=e.oid AND d.deptype='e' WHERE o.oprname='<=>' AND o.oprresult='double precision'::regtype)`

func CheckKnowledgeVectorWithin(ctx context.Context, q transaction.Executor) error {
	var valid bool
	if q == nil || q.Dialect() != transaction.PostgreSQL {
		return errors.New("KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED")
	}
	if e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, ApprovedKnowledgeVectorSQL)).Scan(&valid); e != nil || !valid {
		return errors.New("KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED")
	}
	return nil
}
