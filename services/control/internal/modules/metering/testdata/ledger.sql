-- Candidate ledger shape for the isolated PostgreSQL transaction tests.
CREATE TABLE usage_ledger (
  producer varchar(128) NOT NULL,
  event_id varchar(128) NOT NULL,
  tenant_id uuid NOT NULL,
  workspace_id uuid NOT NULL,
  occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
  gpu_microseconds bigint NOT NULL CHECK (gpu_microseconds >= 0),
  tokens bigint NOT NULL CHECK (tokens >= 0),
  storage_byte_seconds bigint NOT NULL CHECK (storage_byte_seconds >= 0),
  requests bigint NOT NULL CHECK (requests >= 0),
  unmeasured_token_requests bigint NOT NULL DEFAULT 0 CHECK (unmeasured_token_requests BETWEEN 0 AND requests),
  PRIMARY KEY (producer, event_id),
  FOREIGN KEY (tenant_id, workspace_id) REFERENCES workspaces (tenant_id, id),
  CHECK (gpu_microseconds > 0 OR tokens > 0 OR storage_byte_seconds > 0 OR requests > 0)
);
CREATE INDEX usage_ledger_scope_time ON usage_ledger (tenant_id, workspace_id, occurred_at);

CREATE FUNCTION refuse_usage_ledger_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'METERING_APPEND_ONLY' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER usage_ledger_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON usage_ledger
  FOR EACH STATEMENT EXECUTE FUNCTION refuse_usage_ledger_mutation();
