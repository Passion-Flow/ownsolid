-- A person can pin pages to the top of the sidebar. The console and the portal have different pages, so
-- each entrance keeps its own list (UX-124, ADR 0037). Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences
 ADD COLUMN pinned_console jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(pinned_console)='array'),
 ADD COLUMN pinned_portal jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(pinned_portal)='array');
