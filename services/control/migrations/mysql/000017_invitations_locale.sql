ALTER TABLE invitations
    DROP CHECK invitations_chk_1,
    MODIFY locale VARCHAR(35) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL,
    ADD CONSTRAINT invitations_locale_check CHECK(REGEXP_LIKE(locale,'^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$','c'));
