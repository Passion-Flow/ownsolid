package middleware

import (
	"encoding/json"
	"github.com/google/uuid"
	"time"
)

func validID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u != uuid.Nil && u.String() == s
}

type Instance struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ProfileKey  string    `json:"profile_key"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Version     int64     `json:"version"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	Fingerprint string    `json:"-"`
	TenantID    string    `json:"-"`
}

type Operation struct {
	ID          string          `json:"id"`
	InstanceID  string          `json:"instance_id"`
	Kind        string          `json:"kind"`
	State       string          `json:"state"`
	ErrorCode   string          `json:"error_code,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	TenantID    string          `json:"-"`
	WorkspaceID string          `json:"-"`
}

type OperationInput struct {
	Kind                string `json:"kind"`
	Reason              string `json:"reason"`
	IdempotencyKey      string `json:"idempotency_key"`
	Version             int64  `json:"version"`
	BackupID            string `json:"backup_id,omitempty"`
	TargetKey           string `json:"target_key,omitempty"`
	ConfirmIsolated     bool   `json:"confirm_isolated"`
	OriginalOperationID string `json:"original_operation_id,omitempty"`
}

type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type Choice struct {
	Key            string   `json:"key"`
	Kind           string   `json:"kind"`
	Health         bool     `json:"health"`
	Backup         bool     `json:"backup"`
	RestoreTargets []string `json:"restore_targets"`
}

type Observation struct {
	ObservedAt             time.Time `json:"observed_at"`
	Database               string    `json:"database"`
	User                   string    `json:"user"`
	ServerAddress          string    `json:"server_address"`
	ServerPort             int       `json:"server_port"`
	VersionNumber          int       `json:"version_number"`
	InRecovery             bool      `json:"in_recovery"`
	TLSVersion             string    `json:"tls_version"`
	TLSCipher              string    `json:"tls_cipher"`
	Superuser              bool      `json:"superuser"`
	CreateRole             bool      `json:"create_role"`
	CreateDatabase         bool      `json:"create_database"`
	CreateSchema           bool      `json:"create_schema"`
	CreateAnySchemaObjects bool      `json:"create_any_schema_objects"`
	ReadAllTables          bool      `json:"read_all_tables"`
	WriteAnyTable          bool      `json:"write_any_table"`
	TableCount             int64     `json:"table_count"`
	PlatformTenantID       string    `json:"platform_tenant_id,omitempty"`
}
