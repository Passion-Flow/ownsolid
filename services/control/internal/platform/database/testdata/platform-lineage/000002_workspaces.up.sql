-- M02 additive workspace lifecycle, fixed roles and an actual scoped note resource.
ALTER TABLE workspaces ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991);
ALTER TABLE workspaces ADD COLUMN updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE UNIQUE INDEX workspaces_tenant_name_idx ON workspaces(tenant_id, name COLLATE "C");
ALTER TABLE workspace_memberships DROP CONSTRAINT workspace_memberships_role_check;
ALTER TABLE workspace_memberships ADD CONSTRAINT workspace_memberships_role_check CHECK (role IN ('owner','member','viewer'));
ALTER TABLE workspace_memberships ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991);
ALTER TABLE workspace_memberships ADD COLUMN updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE INDEX memberships_scope_idx ON workspace_memberships(tenant_id,workspace_id,user_id);
CREATE TABLE workspace_notes (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    title varchar(100) NOT NULL,
    text text NOT NULL CHECK (octet_length(text)<=8192),
    author_user_id uuid NOT NULL REFERENCES users(id),
    version bigint NOT NULL DEFAULT 1 CHECK (version BETWEEN 1 AND 9007199254740991),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamptz,
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE INDEX notes_scope_list_idx ON workspace_notes(tenant_id,workspace_id,created_at DESC,id DESC) WHERE deleted_at IS NULL;
