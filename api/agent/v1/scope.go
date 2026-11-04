package v1

import "regexp"

// DefaultSiteID and DefaultCellID name the one site and cell of an installation until
// multi-cell installs add configured identities (ARC-202, ARC-301).
const (
	DefaultSiteID = "default"
	DefaultCellID = "default"
)

var scopeIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// Scope locates a resource in the fleet. Every cell-owned resource has exactly one.
type Scope struct {
	SiteID string `json:"site_id"`
	CellID string `json:"cell_id"`
}

// LocalScope is the scope of this installation's only cell.
func LocalScope() Scope { return Scope{SiteID: DefaultSiteID, CellID: DefaultCellID} }

// Valid reports whether both IDs have the immutable identifier form.
func (s Scope) Valid() bool {
	return scopeIDPattern.MatchString(s.SiteID) && scopeIDPattern.MatchString(s.CellID)
}

// Path is the path segment that addresses this scope.
func (s Scope) Path() string { return "/sites/" + s.SiteID + "/cells/" + s.CellID }
