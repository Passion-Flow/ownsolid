ALTER TABLE sessions
    ADD COLUMN sso_tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD COLUMN sso_connector_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD COLUMN sso_binding_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD COLUMN sso_auth_version BIGINT,
    ADD COLUMN sso_flow_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    ADD CONSTRAINT sessions_sso_source_complete CHECK(
      (sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL)
      OR (sso_tenant_id IS NOT NULL AND sso_connector_id IS NOT NULL AND sso_binding_id IS NOT NULL AND sso_auth_version IS NOT NULL AND sso_auth_version BETWEEN 1 AND 9007199254740991 AND sso_flow_id IS NOT NULL)),
    ADD CONSTRAINT sessions_sso_binding_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_binding_id,user_id) REFERENCES sso_bindings(tenant_id,connector_id,id,user_id),
    ADD CONSTRAINT sessions_sso_flow_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_flow_id,user_id) REFERENCES sso_flows(tenant_id,connector_id,id,authenticated_user_id),
    ADD UNIQUE KEY sessions_sso_flow_unique(sso_flow_id),
    ADD KEY sessions_sso_connector_live_idx(sso_tenant_id,sso_connector_id,revoked_at),
    ADD KEY sessions_sso_binding_live_idx(sso_binding_id,revoked_at);
