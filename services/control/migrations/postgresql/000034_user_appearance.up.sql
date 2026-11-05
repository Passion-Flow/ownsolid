-- The theme and density a user chooses follow the account across browsers (UX-118).
-- `system` means the browser follows the operating system. Rows still at revision 1 were
-- seeded, never chosen, so they move to `system`; the revision trigger requires the step.
ALTER TABLE user_preferences DROP CONSTRAINT user_preferences_theme_check;
ALTER TABLE user_preferences ADD CONSTRAINT user_preferences_theme_check CHECK(theme IN ('system','light','dark'));
ALTER TABLE user_preferences ALTER COLUMN theme SET DEFAULT 'system';
ALTER TABLE user_preferences ADD COLUMN density varchar(12) NOT NULL DEFAULT 'comfortable' CONSTRAINT user_preferences_density_check CHECK(density IN ('comfortable','compact'));
UPDATE user_preferences SET theme='system',revision=2,updated_at=clock_timestamp() WHERE revision=1;
