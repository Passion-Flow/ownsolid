-- A person can keep the sidebar to its icons, and the choice follows the account (UX-123, ADR 0037).
-- Existing rows keep their revision: nobody chose this.
ALTER TABLE user_preferences ADD COLUMN navigation_collapsed boolean NOT NULL DEFAULT false;
