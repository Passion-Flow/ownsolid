CREATE TABLE users (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    email_canonical VARCHAR(254) NOT NULL UNIQUE,
    email_display VARCHAR(254) NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    locale VARCHAR(16) NOT NULL CHECK(locale IN('en','zh-CN')),
    status VARCHAR(16) NOT NULL CHECK(status IN('active','disabled')),
    is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE CHECK(is_platform_admin IN(0,1)),
    email_verified_at DATETIME(6),
    auth_epoch BIGINT NOT NULL DEFAULT 1 CHECK(auth_epoch BETWEEN 1 AND 9007199254740991),
    version BIGINT NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY users_list_idx(created_at DESC,id DESC),
    KEY users_status_list_idx(status,created_at DESC,id DESC),
    CHECK(email_canonical=LOWER(email_canonical)),
    CONSTRAINT users_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
