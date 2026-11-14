//go:build aws_integration

package http_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestAWSActualHTTPAuthorityAndRecoveryReads(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Fatal("AWS_EXPLICIT_ISOLATED_DATABASE_REQUIRED")
	}
	accounts := fixture.New(t)
	password := fixture.Bootstrap(t, accounts)
	a := fixture.Login(t, accounts, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	workspace := a.Me.Workspace.ID
	accounts.Config.AWS = config.DefaultAWSConfiguration()
	handler := transport.New(accounts)
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/cloud-inventory/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	portal := httpfixture.New(t, server, "portal", schemas)
	portal.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	base := "/api/v1/admin/workspaces/" + workspace + "/aws"
	portalBase := "/api/v1/workspaces/" + workspace + "/aws"
	initial := admin.Call("GET", base, nil, 200, "InventoryResponse")
	if initial["enabled"] != false {
		t.Fatal("default AWS network enablement")
	}
	input := map[string]any{"connection": "fixture", "region": "us-east-1", "idempotency_key": uuid.NewString()}
	admin.Write("POST", base+"/imports", input, 409, "Problem")
	admin.CSRF = ""
	admin.Call("POST", base+"/imports", input, 403, "Problem")
	portal.Call("GET", base, nil, 404, "Problem")
	// The original dispatcher hides Admin paths from Portal with404. A current
	// Admin reaching the module's Portal route is explicitly denied with403.
	admin.Call("GET", portalBase, nil, 403, "Problem")
	resource := cloudinventory.Instance{InstanceID: "i-0123456789abcdef0", InstanceType: "t3.micro", State: "running", AvailabilityZone: "us-east-1a"}
	raw, _ := json.Marshal(resource)
	if _, e := accounts.DB.Exec(ctx, `INSERT INTO aws_discovered_resources(id,tenant_id,workspace_id,account_id,region,instance_id,observation,observed_at) VALUES($1,$2,$3,'123456789012','us-east-1',$4,$5,$6)`, uuid.NewString(), a.Me.Workspace.TenantID, workspace, resource.InstanceID, raw, time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, client := range []*httpfixture.Client{admin, portal} {
		path := base
		if client == portal {
			path = portalBase
		}
		out := client.Call("GET", path, nil, 200, "InventoryResponse")
		if len(out["items"].([]any)) != 1 {
			t.Fatal("retained HTTP inventory read")
		}
		client.Call("GET", path+"?tenant_id=foreign", nil, 400, "Problem")
		client.Call("GET", path+"/imports/"+uuid.NewString(), nil, 404, "Problem")
	}
}
