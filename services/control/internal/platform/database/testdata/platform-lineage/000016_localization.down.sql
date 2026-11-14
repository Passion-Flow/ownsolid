DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM users WHERE locale NOT IN ('en','zh-CN'))
 OR EXISTS (SELECT 1 FROM invitations WHERE locale NOT IN ('en','zh-CN'))
 OR EXISTS (SELECT 1 FROM mail_deliveries WHERE locale NOT IN ('en','zh-CN')) THEN
  RAISE EXCEPTION 'LOCALIZATION_DOWNGRADE_IN_USE';
 END IF;
END $$;
ALTER TABLE users DROP CONSTRAINT users_locale_check;
ALTER TABLE invitations DROP CONSTRAINT invitations_locale_check;
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_locale_check;
ALTER TABLE users ALTER COLUMN locale TYPE varchar(16);
ALTER TABLE invitations ALTER COLUMN locale TYPE varchar(16);
ALTER TABLE mail_deliveries ALTER COLUMN locale TYPE varchar(16);
ALTER TABLE users ADD CONSTRAINT users_locale_check CHECK (locale IN ('en','zh-CN'));
ALTER TABLE invitations ADD CONSTRAINT invitations_locale_check CHECK (locale IN ('en','zh-CN'));
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_locale_check CHECK (locale IN ('en','zh-CN'));
