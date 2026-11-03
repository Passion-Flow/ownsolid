-- Account profile only: retain the current identity wire/storage shape without an SSO runtime.
ALTER TABLE sessions ADD COLUMN sso_tenant_id uuid,
 ADD COLUMN sso_connector_id uuid, ADD COLUMN sso_binding_id uuid,
 ADD COLUMN sso_auth_version bigint, ADD COLUMN sso_flow_id uuid,
 ADD CONSTRAINT account_local_session_only CHECK(sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL);
