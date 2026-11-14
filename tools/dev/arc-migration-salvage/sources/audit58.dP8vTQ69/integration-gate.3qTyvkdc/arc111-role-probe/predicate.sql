PREPARE gateway_check(boolean,int,int,int) AS WITH r AS (
 SELECT * FROM pg_roles WHERE rolname=current_user
), objects AS (
 SELECT c.oid,c.relname,c.relowner,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN ('r','p','v','m','f') AND NOT c.relispartition AND n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
)
SELECT CASE WHEN r.rolsuper THEN $1::boolean ELSE
 NOT r.rolinherit AND NOT r.rolcreatedb AND NOT r.rolcreaterole AND r.rolcanlogin AND NOT r.rolreplication AND NOT r.rolbypassrls AND r.rolconnlimit BETWEEN $2 AND $3 AND r.rolvaliduntil IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid OR m.roleid=r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_shdepend d WHERE d.refclassid='pg_authid'::regclass AND d.refobjid=r.oid AND d.deptype='o')
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(r.oid,c.oid,'USAGE,SELECT,UPDATE') ELSE false END)
 AND has_database_privilege(r.oid,current_database(),'CONNECT') AND NOT has_database_privilege(r.oid,current_database(),'CREATE')
 AND has_schema_privilege(r.oid,current_schema(),'USAGE') AND NOT has_schema_privilege(r.oid,current_schema(),'CREATE,USAGE WITH GRANT OPTION')
 AND (SELECT count(*) FROM objects WHERE nspname=current_schema())=$4
 AND NOT EXISTS(SELECT 1 FROM objects o WHERE CASE WHEN o.nspname=current_schema() THEN
  o.relowner=r.oid OR CASE WHEN o.relname='audit_chain_head' THEN
   has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
   OR has_any_column_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,REFERENCES')
  ELSE NOT has_table_privilege(r.oid,o.oid,'SELECT')
  OR has_table_privilege(r.oid,o.oid,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION')
  OR CASE WHEN o.relname='schema_migrations' THEN has_table_privilege(r.oid,o.oid,'INSERT,UPDATE,DELETE')
    ELSE NOT has_table_privilege(r.oid,o.oid,'INSERT') OR NOT has_table_privilege(r.oid,o.oid,'UPDATE') OR NOT has_table_privilege(r.oid,o.oid,'DELETE') END END
 ELSE has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') END)
 END FROM r;
EXECUTE gateway_check(false,1,1000,118);
