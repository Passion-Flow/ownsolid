package productagent

import (
	_ "embed"
	json "encoding/json/v2"
	"errors"
	"net/http"
)

//go:generate go run ./internal/cataloggen

//go:embed gen/tools.json
var generatedToolCatalog []byte

type tool struct {
	OperationID string   `json:"operation_id"`
	Risk        string   `json:"risk"`
	Permission  string   `json:"permission"`
	Method      string   `json:"method"`
	Audiences   []string `json:"audiences"`
}

func loadTools() (map[string]tool, error) {
	var catalog struct {
		Tools map[string]tool `json:"tools"`
	}
	if err := json.Unmarshal(generatedToolCatalog, &catalog); err != nil {
		return nil, err
	}
	// Each enabled adapter must be present in the reviewed catalog and vice versa.
	applications := catalog.Tools[listApplications]
	conversations := catalog.Tools[listConversations]
	if len(catalog.Tools) != 2 || applications.OperationID != "ListApplications" || conversations.OperationID != "ListApplicationConversations" ||
		!applications.allowsRead("portal") || !conversations.allowsRead("portal") {
		return nil, errors.New("PRODUCT_AGENT_TOOL_CATALOG_INVALID")
	}
	return catalog.Tools, nil
}

func (t tool) allowsRead(audience string) bool {
	return t.Risk == "read" && t.Permission == "workspace_member" && t.Method == http.MethodGet && len(t.Audiences) == 1 && t.Audiences[0] == audience
}
