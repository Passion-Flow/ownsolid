ALTER TABLE mail_deliveries
    DROP CHECK mail_deliveries_chk_2,
    MODIFY locale VARCHAR(35) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL,
    ADD CONSTRAINT mail_deliveries_locale_check CHECK(REGEXP_LIKE(locale,'^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$','c'));
