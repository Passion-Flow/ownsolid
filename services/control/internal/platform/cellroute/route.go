// Package cellroute is the cell router at the API boundary. Public paths name the site
// and cell of a cell-owned resource; modules below this boundary serve one cell and keep
// cell-relative routes. Multi-cell installs forward to other cells from here (ARC-206).
package cellroute

import (
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const (
	apiRoot     = "/api/v1"
	agentRoot   = "/agent/v1"
	gatewayRoot = "/gateway/v1"
)

// Families below /api/v1/admin and /api/v1/workspaces/{id} whose resources a cell owns.
// Everything else in those trees belongs to the global layer: identity, tenancy, audit
// aggregation, licensing and release state.
var cellAdmin = families("agent-pacing", "artifact-objects", "datasets", "deployments", "distribution", "image-registry",
	"knowledge-bases", "model-holdouts", "model-imports", "model-preparations", "models", "monitoring",
	"node-join-tokens", "nodes", "plugins", "private-clusters", "task-delivery-quarantine", "training", "workflows")
var cellWorkspace = families("artifact-objects", "aws", "datasets", "deployments", "distribution", "image-registry",
	"knowledge-bases", "middleware", "model-holdouts", "model-preparations", "models", "nodes", "plugins",
	"private-clusters", "training", "workflows")
var cellRoot = families("applications", "application-conversations")

func families(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// CellOwned reports whether a cell-relative path addresses a resource a cell owns.
func CellOwned(path string) bool {
	switch {
	case strings.HasPrefix(path, agentRoot+"/nodes/"), strings.HasPrefix(path, gatewayRoot+"/instances/"):
		return true
	case !strings.HasPrefix(path, apiRoot+"/"):
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, apiRoot+"/"), "/")
	switch {
	case parts[0] == "admin" && len(parts) >= 2 && parts[1] == "workspaces":
		return len(parts) >= 4 && cellWorkspace[parts[3]]
	case parts[0] == "admin" && len(parts) >= 2:
		return cellAdmin[parts[1]]
	case parts[0] == "workspaces":
		return len(parts) >= 3 && cellWorkspace[parts[2]]
	}
	return cellRoot[parts[0]]
}

// Resolve returns the cell-relative path for a public path. It refuses another scope,
// a scope on a global path, and a cell-owned path without its scope, so each resource
// has exactly one public path.
func Resolve(path string, local wire.Scope) (string, bool) {
	inner, scope, scoped := split(path)
	if !scoped {
		return path, !CellOwned(path)
	}
	return inner, scope == local && CellOwned(inner)
}

// Public returns the public form of a cell-relative path, which may carry a query.
func Public(path string, scope wire.Scope) string {
	route, _, _ := strings.Cut(path, "?")
	if !CellOwned(route) {
		return path
	}
	for _, root := range []string{apiRoot, agentRoot, gatewayRoot} {
		if rest, found := strings.CutPrefix(path, root+"/"); found {
			return root + scope.Path() + "/" + rest
		}
	}
	return path
}

// Relative removes a well-formed scope without checking it, for labels and budgets.
func Relative(path string) string {
	if inner, _, scoped := split(path); scoped {
		return inner
	}
	return path
}

func split(path string) (string, wire.Scope, bool) {
	for _, root := range []string{apiRoot, agentRoot, gatewayRoot} {
		rest, found := strings.CutPrefix(path, root+"/sites/")
		if !found {
			continue
		}
		parts := strings.SplitN(rest, "/", 4)
		if len(parts) != 4 || parts[1] != "cells" {
			return path, wire.Scope{}, false
		}
		return root + "/" + parts[3], wire.Scope{SiteID: parts[0], CellID: parts[2]}, true
	}
	return path, wire.Scope{}, false
}
