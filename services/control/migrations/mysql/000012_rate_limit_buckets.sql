CREATE TABLE rate_limit_buckets (
    bucket_key VARBINARY(32) PRIMARY KEY CHECK(OCTET_LENGTH(bucket_key)=32),
    window_started_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    attempt_count INT NOT NULL CHECK(attempt_count>=0),
    last_admitted_at DATETIME(6),
    KEY rate_limit_expiry_idx(expires_at),
    CHECK(expires_at>window_started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
