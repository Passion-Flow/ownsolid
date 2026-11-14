-- Active M24 PostgreSQL image registry schema.
CREATE TABLE image_registry_pins (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 asset_id varchar(160) NOT NULL CHECK(length(asset_id)>0),
 source_archive_sha256 char(64) NOT NULL CHECK(source_archive_sha256~'^[a-f0-9]{64}$'),
 source_manifest_sha256 char(64) NOT NULL CHECK(source_manifest_sha256~'^[a-f0-9]{64}$'),
 backend_identity varchar(71) NOT NULL CHECK(backend_identity~'^sha256:[a-f0-9]{64}$'),
 state varchar(16) NOT NULL CHECK(state IN('importing','ready','unknown','deleting','deleted','failed')),
 manifest_digest varchar(71), config_digest varchar(71), archive_sha256 char(64), archive_bytes bigint,
 inventory jsonb, inventory_sha256 char(64),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK((inventory IS NULL)=(manifest_digest IS NULL)), CHECK((inventory IS NULL)=(config_digest IS NULL)),
 CHECK((inventory IS NULL)=(archive_sha256 IS NULL)), CHECK((inventory IS NULL)=(archive_bytes IS NULL)),
 CHECK((inventory IS NULL)=(inventory_sha256 IS NULL)),
 CHECK(manifest_digest IS NULL OR manifest_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(config_digest IS NULL OR config_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(archive_sha256 IS NULL OR archive_sha256~'^[a-f0-9]{64}$'),
 CHECK(inventory_sha256 IS NULL OR inventory_sha256~'^[a-f0-9]{64}$'),
 CHECK(archive_bytes IS NULL OR archive_bytes>0),
 CHECK(inventory IS NULL OR jsonb_typeof(inventory)='object'),
 CHECK(state<>'ready' OR inventory IS NOT NULL)
);
CREATE INDEX image_registry_scope ON image_registry_pins(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE image_registry_blobs (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal>=0), digest varchar(71) NOT NULL CHECK(digest~'^sha256:[a-f0-9]{64}$'),
 size bigint NOT NULL CHECK(size>0), media_type varchar(160) NOT NULL CHECK(length(media_type)>0), kind varchar(16) NOT NULL CHECK(kind IN('manifest','config','layer')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id,ordinal),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_imports (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 owner_token uuid NOT NULL, phase varchar(16) NOT NULL CHECK(phase IN('prepared','remote','completed','unknown')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_deployment_refs (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 node_id uuid NOT NULL, deployment_id uuid NOT NULL, created_revision bigint NOT NULL CHECK(created_revision>=1),
 released_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,deployment_id),
 UNIQUE(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE TABLE image_registry_grants (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 node_id uuid NOT NULL, deployment_id uuid NOT NULL, task_id uuid NOT NULL, revision bigint NOT NULL CHECK(revision>=1),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 -- Immutable historical binding; no session FK may block existing retention.
 created_session_id uuid NOT NULL, created_token_hash bytea NOT NULL CHECK(octet_length(created_token_hash)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), expires_at timestamptz NOT NULL, revoked_at timestamptz,
 FOREIGN KEY(tenant_id,workspace_id,pin_id,node_id,deployment_id) REFERENCES image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(expires_at>created_at)
);
CREATE UNIQUE INDEX image_registry_current_grant ON image_registry_grants(tenant_id,workspace_id,node_id,task_id) WHERE revoked_at IS NULL;
CREATE TABLE image_registry_offline_refs (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, pin_id uuid NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(), released_at timestamptz,
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
);
CREATE TABLE image_registry_mutations (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, actor_id uuid NOT NULL REFERENCES users(id),
 operation varchar(32) NOT NULL CHECK(operation IN('import','reconcile','remove','revoke','offline.hold','offline.release','reference.release','abandon','discard','grant')),
 idempotency_key uuid NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 target_id uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,operation,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE FUNCTION image_registry_pin_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.asset_id,OLD.source_archive_sha256,OLD.source_manifest_sha256,OLD.backend_identity,OLD.created_by,OLD.actor_epoch,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.asset_id,NEW.source_archive_sha256,NEW.source_manifest_sha256,NEW.backend_identity,NEW.created_by,NEW.actor_epoch,NEW.created_at)
 OR OLD.inventory IS NOT NULL AND ROW(OLD.manifest_digest,OLD.config_digest,OLD.archive_sha256,OLD.archive_bytes,OLD.inventory,OLD.inventory_sha256) IS DISTINCT FROM ROW(NEW.manifest_digest,NEW.config_digest,NEW.archive_sha256,NEW.archive_bytes,NEW.inventory,NEW.inventory_sha256)
 OR NEW.revision<>OLD.revision+1 OR OLD.state='deleted' THEN
 RAISE EXCEPTION 'REGISTRY_IMMUTABLE_IDENTITY'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_pin_identity BEFORE UPDATE ON image_registry_pins FOR EACH ROW EXECUTE FUNCTION image_registry_pin_guard();
CREATE FUNCTION image_registry_grant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.node_id,OLD.deployment_id,OLD.task_id,OLD.revision,OLD.created_by,OLD.actor_epoch,OLD.created_session_id,OLD.created_token_hash,OLD.created_at,OLD.expires_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.node_id,NEW.deployment_id,NEW.task_id,NEW.revision,NEW.created_by,NEW.actor_epoch,NEW.created_session_id,NEW.created_token_hash,NEW.created_at,NEW.expires_at)
 OR OLD.revoked_at IS NOT NULL AND OLD.revoked_at IS DISTINCT FROM NEW.revoked_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_GRANT'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_grant_identity BEFORE UPDATE ON image_registry_grants FOR EACH ROW EXECUTE FUNCTION image_registry_grant_guard();
CREATE FUNCTION image_registry_deployment_ref_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.node_id,OLD.deployment_id,OLD.created_revision,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.node_id,NEW.deployment_id,NEW.created_revision,NEW.created_at)
 OR OLD.released_at IS NOT NULL AND OLD.released_at IS DISTINCT FROM NEW.released_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_REFERENCE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_deployment_ref_identity BEFORE UPDATE ON image_registry_deployment_refs FOR EACH ROW EXECUTE FUNCTION image_registry_deployment_ref_guard();
CREATE FUNCTION image_registry_offline_ref_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.pin_id,OLD.created_by,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.pin_id,NEW.created_by,NEW.created_at)
 OR OLD.released_at IS NOT NULL AND OLD.released_at IS DISTINCT FROM NEW.released_at THEN RAISE EXCEPTION 'REGISTRY_IMMUTABLE_OFFLINE_REFERENCE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER image_registry_offline_ref_identity BEFORE UPDATE ON image_registry_offline_refs FOR EACH ROW EXECUTE FUNCTION image_registry_offline_ref_guard();
