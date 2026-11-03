package workspaces

import "time"

type Workspace struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Version   int64     `json:"version"`
	IsDefault bool      `json:"is_default"`
	Role      *string   `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Membership struct {
	UserID      string    `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	UserStatus  string    `json:"user_status"`
	Role        string    `json:"role"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
type Note struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	WorkspaceID  string    `json:"workspace_id"`
	Title        string    `json:"title"`
	Text         string    `json:"text"`
	AuthorUserID string    `json:"author_user_id"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func roleValid(role string) bool { return role == "owner" || role == "member" || role == "viewer" }
