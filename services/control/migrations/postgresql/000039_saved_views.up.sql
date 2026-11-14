-- Saved views of a console list: its filters, sort and columns under a name (UX-103). They
-- belong to one person and follow the account across browsers. The page owns the meaning of
-- `state`; the database only bounds its size and shape.
CREATE TABLE saved_views (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 list_key varchar(64) NOT NULL CHECK (list_key ~ '^[a-z][a-z0-9-]{0,63}$'),
 name varchar(100) NOT NULL CHECK (name = btrim(name) AND length(name) BETWEEN 1 AND 100),
 state jsonb NOT NULL CHECK (jsonb_typeof(state) = 'object' AND octet_length(state::text) <= 4096),
 revision bigint NOT NULL DEFAULT 1 CHECK (revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT saved_views_name_unique UNIQUE (user_id, list_key, name)
);
