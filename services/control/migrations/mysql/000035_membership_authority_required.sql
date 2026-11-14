ALTER TABLE workspace_memberships
    MODIFY COLUMN authority_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT (UUID()),
    ADD CONSTRAINT membership_authority_canonical CHECK(REGEXP_LIKE(authority_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'));
