ALTER TABLE users DROP CONSTRAINT users_locale_check;
ALTER TABLE invitations DROP CONSTRAINT invitations_locale_check;
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_locale_check;
ALTER TABLE users ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE invitations ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE mail_deliveries ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE users ADD CONSTRAINT users_locale_check CHECK (locale ~ '^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$');
ALTER TABLE invitations ADD CONSTRAINT invitations_locale_check CHECK (locale ~ '^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$');
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_locale_check CHECK (locale ~ '^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$');
