CREATE TABLE terminal_notifications (
 id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT (UUID()) PRIMARY KEY,
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workflow_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
 recipient_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 outcome VARCHAR(16) NOT NULL CHECK(outcome IN('succeeded','failed','cancelled','interrupted')),
 error_code VARCHAR(100) CHARACTER SET ascii COLLATE ascii_bin CHECK(error_code IS NULL OR REGEXP_LIKE(error_code,'^[A-Z][A-Z0-9_]{0,99}$','c')),
 occurred_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 read_at DATETIME(6),
 KEY terminal_notification_recipient_page_idx(recipient_id,occurred_at DESC,id DESC),
 KEY terminal_notification_unread_idx(recipient_id,read_at,occurred_at DESC,id DESC),
 KEY terminal_notification_retention_idx(tenant_id,occurred_at,id),
 CHECK((outcome='succeeded')=(error_code IS NULL)),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id,recipient_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id,owner_id) ON DELETE CASCADE,
 CONSTRAINT terminal_notifications_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT terminal_notification_read_order CHECK(read_at IS NULL OR read_at>=occurred_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
