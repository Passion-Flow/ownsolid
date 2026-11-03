package identity

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	Locale          string     `json:"locale"`
	Status          string     `json:"status"`
	IsPlatformAdmin bool       `json:"is_platform_admin"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	Version         int64      `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Epoch           int64      `json:"-"`
}
type Workspace struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Role     string `json:"role"`
}
type Me struct {
	User      User       `json:"user"`
	Workspace *Workspace `json:"workspace"`
	Audience  string     `json:"audience"`
}
type Auth struct {
	Me        Me
	SessionID string
	TokenHash []byte
	Epoch     int64
}

// MaximumAuthVersion is the exact JSON-safe integer domain used by both schemas.
const MaximumAuthVersion int64 = 1<<53 - 1

const userColumns = "id::text,email_display,display_name,locale,status,is_platform_admin,email_verified_at,version,created_at,updated_at,auth_epoch"

func scanUser(row pgx.Row) (User, error) {
	var u User
	e := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Locale, &u.Status, &u.IsPlatformAdmin, &u.EmailVerifiedAt, &u.Version, &u.CreatedAt, &u.UpdatedAt, &u.Epoch)
	return u, e
}
func code(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func failure(v string) error { return errors.New(v) }
