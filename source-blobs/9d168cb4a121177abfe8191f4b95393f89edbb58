-- Unnumbered PostgreSQL candidate for ARC. Apply to the isolated fixture only.
CREATE TABLE artifact_store_bindings (
    store_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    site_id varchar(32) NOT NULL DEFAULT local_site_id()
        CHECK (site_id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    cell_id varchar(32) NOT NULL DEFAULT local_cell_id()
        CHECK (cell_id ~ '^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$'),
    site_sha256 text NOT NULL CHECK (site_sha256 ~ '^[0-9a-f]{64}$'),
    backend_sha256 text NOT NULL CHECK (backend_sha256 ~ '^[0-9a-f]{64}$'),
    UNIQUE (store_id, tenant_id, site_id, cell_id, site_sha256, backend_sha256)
);

CREATE TABLE artifact_backup_holds (
    system_identifier text NOT NULL CHECK (system_identifier ~ '^[1-9][0-9]{0,19}$'),
    database_oid oid NOT NULL CHECK (database_oid <> 0),
    tenant_id uuid NOT NULL,
    store_id uuid NOT NULL,
    site_id varchar(32) NOT NULL DEFAULT local_site_id(),
    cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
    site_sha256 text NOT NULL CHECK (site_sha256 ~ '^[0-9a-f]{64}$'),
    backend_sha256 text NOT NULL CHECK (backend_sha256 ~ '^[0-9a-f]{64}$'),
    generation bigint NOT NULL DEFAULT 0 CHECK (generation >= 0),
    state text NOT NULL DEFAULT 'released' CHECK (state IN ('held', 'confirmed', 'released')),
    operation_id uuid,
    owner_id uuid,
    lease_until timestamptz,
    inventory_sha256 text CHECK (inventory_sha256 ~ '^[0-9a-f]{64}$'),
    copy_id uuid,
    archive_sha256 text CHECK (archive_sha256 ~ '^[0-9a-f]{64}$'),
    archive_bytes bigint CHECK (archive_bytes > 0),
    PRIMARY KEY (system_identifier, database_oid, tenant_id, store_id),
    FOREIGN KEY (store_id, tenant_id, site_id, cell_id, site_sha256, backend_sha256)
        REFERENCES artifact_store_bindings
            (store_id, tenant_id, site_id, cell_id, site_sha256, backend_sha256),
    CHECK (num_nonnulls(inventory_sha256, copy_id, archive_sha256, archive_bytes)
           IN (0, 4)),
    CHECK (
        (generation = 0 AND state = 'released'
            AND num_nonnulls(operation_id, owner_id, lease_until, copy_id) = 0)
        OR
        (generation > 0 AND operation_id IS NOT NULL AND owner_id IS NOT NULL
            AND ((state = 'held' AND lease_until IS NOT NULL AND copy_id IS NULL)
                OR (state = 'confirmed' AND lease_until IS NOT NULL AND copy_id IS NOT NULL)
                OR (state = 'released' AND lease_until IS NULL AND copy_id IS NOT NULL)))
    )
);
