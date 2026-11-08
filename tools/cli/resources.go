package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type resource struct {
	key      string
	singular string
	columns  []column
}

var (
	nodes = resource{"nodes", "node", []column{
		{"NAME", "name"}, {"ID", "id"}, {"STATUS", "status"}, {"CONNECTIVITY", "connectivity"}, {"WORKSPACE", "workspace_id"},
	}}
	models = resource{"models", "model", []column{
		{"NAME", "name"}, {"ID", "id"}, {"KIND", "kind"}, {"STATE", "state"},
	}}
	deployments = resource{"deployments", "deployment", []column{
		{"ID", "id"}, {"MODEL ID", "model_id"}, {"NODE ID", "node_id"}, {"DESIRED", "desired_state"}, {"OBSERVED", "observed_state"},
	}}
)

func (cfg settings) adminPath(r resource) string {
	return "/api/v1/sites/" + cfg.SiteID + "/cells/" + cfg.CellID + "/admin/" + r.key
}

func (c *apiClient) workspace(ctx context.Context, cfg settings, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if cfg.WorkspaceID != "" {
		return cfg.WorkspaceID, nil
	}
	var me struct {
		Workspace *struct {
			ID string `json:"id"`
		} `json:"workspace"`
	}
	if err := c.get(ctx, "/api/v1/me", nil, &me); err != nil {
		return "", err
	}
	if me.Workspace == nil || !resourceID.MatchString(me.Workspace.ID) {
		return "", fmt.Errorf("the Admin session has no current workspace; choose one with --workspace or workspace_id in config.json")
	}
	return me.Workspace.ID, nil
}

type listPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

func (c *apiClient) list(ctx context.Context, path string, query url.Values, paginated bool) ([]json.RawMessage, error) {
	if paginated {
		query.Set("limit", "100")
	}
	items := make([]json.RawMessage, 0)
	seen := make(map[string]bool)
	for {
		var page listPage
		if err := c.get(ctx, path, query, &page); err != nil {
			return nil, err
		}
		if page.Items == nil {
			return nil, fmt.Errorf("invalid list response from %s; expected an items array", c.origin.String())
		}
		items = append(items, page.Items...)
		if !paginated || page.NextCursor == "" {
			return items, nil
		}
		if seen[page.NextCursor] {
			return nil, fmt.Errorf("server %s repeated a list cursor; retry or check server logs", c.origin.String())
		}
		seen[page.NextCursor] = true
		query.Set("cursor", page.NextCursor)
	}
}

func (c *apiClient) resolveNode(ctx context.Context, cfg settings, name string) (string, error) {
	query := url.Values{"name_prefix": {name}}
	items, err := c.list(ctx, cfg.adminPath(nodes), query, true)
	if err != nil {
		return "", err
	}
	return uniqueResourceID(items, name, nodes.singular)
}

func (c *apiClient) resolveDeployment(ctx context.Context, cfg settings, workspace, name string) (string, error) {
	var runtime struct {
		UI struct {
			LookupPageSize int `json:"lookup_page_size"`
		} `json:"ui"`
	}
	if err := c.get(ctx, "/runtime-config.json", nil, &runtime); err != nil {
		return "", fmt.Errorf("cannot verify name lookup bounds; use a deployment UUID: %w", err)
	}
	limit := runtime.UI.LookupPageSize
	if limit < 1 {
		return "", fmt.Errorf("the server did not report its name lookup limit; use a deployment UUID")
	}
	query := url.Values{"workspace_id": {workspace}}
	items, err := c.list(ctx, cfg.adminPath(models), query, false)
	if err != nil {
		return "", err
	}
	// A full, unpaginated list cannot prove that a name is unique.
	if len(items) >= limit {
		return "", fmt.Errorf("the workspace model list reached the server lookup limit; use a deployment UUID")
	}
	modelID, err := uniqueResourceID(items, name, models.singular)
	if err != nil {
		return "", fmt.Errorf("%w; you can select a deployment directly by UUID", err)
	}
	query.Set("model_id", modelID)
	query.Set("retained", "true")
	items, err = c.list(ctx, cfg.adminPath(deployments), query, false)
	if err != nil {
		return "", err
	}
	switch len(items) {
	case 0:
		return "", fmt.Errorf("no retained deployment for model %q in the selected workspace", name)
	case 1:
		if limit == 1 {
			return "", fmt.Errorf("the deployment list reached the server lookup limit; use a deployment UUID")
		}
		return readResourceID(items[0])
	default:
		return "", fmt.Errorf("model %q has multiple deployments; use ownsolid deployment list and select a deployment UUID", name)
	}
}

func uniqueResourceID(items []json.RawMessage, name, kind string) (string, error) {
	var match string
	for _, item := range items {
		var row struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(item, &row); err != nil || !resourceID.MatchString(row.ID) {
			return "", fmt.Errorf("invalid %s in the server's list response", kind)
		}
		if row.Name == name {
			if match != "" && row.ID != match {
				return "", fmt.Errorf("more than one %s is named %q; select by UUID", kind, name)
			}
			match = row.ID
		}
	}
	if match == "" {
		return "", fmt.Errorf("no %s named %q in the selected scope", kind, name)
	}
	return match, nil
}

func readResourceID(item json.RawMessage) (string, error) {
	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(item, &row); err != nil || !resourceID.MatchString(row.ID) {
		return "", fmt.Errorf("the server returned an invalid resource ID")
	}
	return row.ID, nil
}

func lookupID(name string) string {
	id := strings.ToLower(name)
	if resourceID.MatchString(id) {
		return id
	}
	return ""
}
