-- The language a person chooses follows the account, and an installation can set the language
-- for those who have not chosen (UX-109). `auto` means no choice: the tenant's default, then
-- the browser's languages. Existing accounts start there, so nobody's language changes.
ALTER TABLE user_preferences ADD COLUMN locale varchar(16) NOT NULL DEFAULT 'auto' CONSTRAINT user_preferences_locale_check CHECK (locale IN ('auto', 'en', 'zh-CN'));
ALTER TABLE tenants ADD COLUMN default_locale varchar(16) NOT NULL DEFAULT 'auto' CONSTRAINT tenants_default_locale_check CHECK (default_locale IN ('auto', 'en', 'zh-CN'));
ALTER TABLE tenants ADD COLUMN settings_revision bigint NOT NULL DEFAULT 1 CONSTRAINT tenants_settings_revision_check CHECK (settings_revision BETWEEN 1 AND 9007199254740991);
