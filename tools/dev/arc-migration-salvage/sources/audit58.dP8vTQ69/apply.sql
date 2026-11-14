BEGIN;
\i /repo/services/control/migrations/postgresql/000058_audit_chain_head_scope.up.sql
INSERT INTO schema_migrations(version,checksum) VALUES(58,'c4d228212ca9a4107dc5e4a78468f9f7a35dfba818564d406a0a8861cdca1413');
COMMIT;
