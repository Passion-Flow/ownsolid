package sso

import "time"

const (
	ProviderGitea = "gitea"
	ModeLogin     = "login"
	ModeLink      = "link"
	flowPending   = "pending"
	flowClaimed   = "claimed"
	flowSucceeded = "succeeded"
)

type Connector struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Provider    string    `json:"provider"`
	Origin      string    `json:"origin"`
	ClientID    string    `json:"client_id"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
	AuthVersion int64     `json:"auth_version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateConnector struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Origin       string `json:"origin"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Enabled      bool   `json:"enabled"`
	Reason       string `json:"reason"`
}

type UpdateConnector struct {
	Revision     int64   `json:"revision"`
	Name         string  `json:"name"`
	Enabled      bool    `json:"enabled"`
	ClientID     *string `json:"client_id"`
	ClientSecret *string `json:"client_secret"`
	Reason       string  `json:"reason"`
}

type PublicConnector struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type ConnectorPage struct {
	Items      []Connector `json:"items"`
	NextCursor *string     `json:"next_cursor"`
	// UnreadableConnectorIDs identifies affected rows on this authenticated page.
	// The published HTTP contract does not yet expose this runtime state.
	UnreadableConnectorIDs map[string]bool `json:"-"`
}

type Binding struct {
	ID          string    `json:"id"`
	ConnectorID string    `json:"connector_id"`
	Name        string    `json:"name"`
	Provider    string    `json:"provider"`
	Subject     string    `json:"subject"`
	Active      bool      `json:"active"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UnlinkBinding struct {
	Revision int64  `json:"revision"`
	Password string `json:"password"`
	Reason   string `json:"reason"`
}

type StartInput struct {
	Mode     string  `json:"mode"`
	Password *string `json:"password"`
	Reason   *string `json:"reason"`
}

type StartResult struct {
	AuthorizationURL string    `json:"authorization_url"`
	ExpiresAt        time.Time `json:"expires_at"`
	BrowserToken     string    `json:"-"`
}
