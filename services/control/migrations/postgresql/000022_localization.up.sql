-- Forward-only account22: immutable account1-21 remain unchanged.
ALTER TABLE users DROP CONSTRAINT users_locale_check;
ALTER TABLE users ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE users ADD CONSTRAINT users_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');
ALTER TABLE invitations DROP CONSTRAINT invitations_locale_check;
ALTER TABLE invitations ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE invitations ADD CONSTRAINT invitations_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_locale_check;
ALTER TABLE mail_deliveries ALTER COLUMN locale TYPE varchar(35);
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_locale_check CHECK (length(locale) BETWEEN 2 AND 35 AND locale COLLATE "C" ~ '^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?(-([a-z0-9]{5,8}|[0-9][a-z0-9]{3})){0,3}$');
