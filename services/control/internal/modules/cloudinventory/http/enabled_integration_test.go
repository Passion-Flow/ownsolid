//go:build aws_integration

package http_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

type enabledAWSHTTP struct {
	accounts              *identity.Service
	auth                  identity.Auth
	handler               *transport.Handler
	admin, portal         *httpfixture.Client
	provider              *awsProtocolTLS
	adminBase, portalBase string
}

func newEnabledAWSHTTP(t *testing.T) *enabledAWSHTTP {
	t.Helper()
	f := &enabledAWSHTTP{accounts: fixture.New(t)}
	password := fixture.Bootstrap(t, f.accounts)
	f.auth = fixture.Login(t, f.accounts, "admin@example.invalid", password, "admin")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(root, "credentials.json")
	raw, err := json.Marshal(cloudinventory.Credentials{AccessKeyID: awsTestAccessKey, SecretAccessKey: awsTestSecret})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(credentials, raw, 0600); err != nil {
		t.Fatal(err)
	}
	policy := config.DefaultAWSConfiguration()
	policy.Enabled, policy.RetryAttempts = true, 1
	policy.Connections = []config.AWSConnection{{Name: "fixture", Partition: "aws", AccountID: awsTestAccount, Regions: []string{awsTestRegion}, WorkspaceIDs: []string{f.auth.Me.Workspace.ID}, CredentialsFile: credentials}}
	if err = policy.Validate(); err != nil {
		t.Fatal(err)
	}
	f.accounts.Config.AWS = policy
	f.provider = newAWSProtocolTLS(t)
	f.handler = transport.New(f.accounts)
	server := httpfixture.NewTLSServer(t, f.handler)
	t.Cleanup(server.Close)
	schemas := httpfixture.Schemas(t, "../../../../../../api/openapi/v1/cloud-inventory/openapi.json")
	f.admin = httpfixture.New(t, server, "admin", schemas)
	f.portal = httpfixture.New(t, server, "portal", schemas)
	for _, client := range []*httpfixture.Client{f.admin, f.portal} {
		client.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	f.adminBase = "/api/v1/admin/workspaces/" + f.auth.Me.Workspace.ID + "/aws"
	f.portalBase = "/api/v1/workspaces/" + f.auth.Me.Workspace.ID + "/aws"
	return f
}

func awsImportBody(key string) map[string]any {
	return map[string]any{"connection": "fixture", "region": awsTestRegion, "idempotency_key": key}
}

func awsProblem(t *testing.T, value map[string]any, expected string) {
	t.Helper()
	if value["code"] != expected {
		t.Fatalf("expected public code %s, got %v", expected, value["code"])
	}
}

func (f *enabledAWSHTTP) counts(t *testing.T) [3]int {
	t.Helper()
	var out [3]int
	if err := f.accounts.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM aws_import_receipts),(SELECT count(*) FROM audit_events WHERE action='aws.inventory_imported'),(SELECT count(*) FROM managed_nodes)`).Scan(&out[0], &out[1], &out[2]); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *enabledAWSHTTP) baseline(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	initial := f.admin.Call("GET", f.adminBase, nil, 200, "InventoryResponse")
	if initial["enabled"] != true || initial["can_import"] != true || len(initial["connections"].([]any)) != 1 {
		t.Fatal("configured import choices missing")
	}
	key := uuid.NewString()
	receipt := f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 200, "ReceiptResponse")
	if receipt["idempotency_key"] != key || receipt["account_id"] != awsTestAccount || receipt["region"] != awsTestRegion || receipt["imported"] != float64(2) || f.provider.calls.Load() != 3 {
		t.Fatal("actual TLS import receipt/provider pagination")
	}
	inventory := f.admin.Call("GET", f.adminBase, nil, 200, "InventoryResponse")
	if len(inventory["items"].([]any)) != 2 || f.counts(t) != [3]int{1, 1, 0} {
		t.Fatal("actual persisted inventory/receipt/audit/node bounds")
	}
	for _, item := range inventory["items"].([]any) {
		if item.(map[string]any)["missing"] != false {
			t.Fatal("new inventory marked missing")
		}
	}
	t.Log("AWS_ENABLED_BASELINE: verified TLS/SNI/SigV4, two EC2 pages, persisted2, receipt1, audit1, managed_nodes0")
	return receipt, inventory
}

func (f *enabledAWSHTTP) unchanged(t *testing.T, before map[string]any, counts [3]int, key string) {
	t.Helper()
	if after := f.admin.Call("GET", f.adminBase, nil, 200, "InventoryResponse"); !reflect.DeepEqual(before, after) || f.counts(t) != counts {
		t.Fatal("failed import changed durable inventory/receipt/audit")
	}
	awsProblem(t, f.admin.Call("GET", f.adminBase+"/imports/"+key, nil, 404, "Problem"), "RESOURCE_NOT_FOUND")
}

func TestAWSActualHTTPEnabledImport(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DSN_FILE") == "" && os.Getenv("PROJECT_ONE_TEST_DSN") == "" {
		t.Fatal("AWS_EXPLICIT_ISOLATED_DATABASE_REQUIRED")
	}
	t.Run("verified_tls_import_and_replay", func(t *testing.T) {
		f := newEnabledAWSHTTP(t)
		receipt, inventory := f.baseline(t)
		key := receipt["idempotency_key"].(string)
		before := f.provider.calls.Load()
		for _, entrance := range []struct {
			client *httpfixture.Client
			base   string
		}{{f.admin, f.adminBase}, {f.portal, f.portalBase}} {
			if actual := entrance.client.Call("GET", entrance.base, nil, 200, "InventoryResponse"); !reflect.DeepEqual(inventory, actual) {
				t.Fatal("dual entrance persisted inventory differs")
			}
			if actual := entrance.client.Call("GET", entrance.base+"/imports/"+key, nil, 200, "ReceiptResponse"); !reflect.DeepEqual(receipt, actual) {
				t.Fatal("actor-bound receipt changed")
			}
			if actual := entrance.client.Write("POST", entrance.base+"/imports", awsImportBody(key), 200, "ReceiptResponse"); !reflect.DeepEqual(receipt, actual) {
				t.Fatal("exact import replay changed result")
			}
		}
		conflict := awsImportBody(key)
		conflict["connection"] = "other"
		awsProblem(t, f.admin.Write("POST", f.adminBase+"/imports", conflict, 409, "Problem"), "IDEMPOTENCY_CONFLICT")
		foreign, err := f.handler.AWS.Service.Workspaces.Create(context.Background(), f.auth, "Synthetic ungranted AWS Workspace", f.auth.Me.User.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		foreignBase := "/api/v1/admin/workspaces/" + foreign.ID + "/aws"
		foreignRead := f.admin.Call("GET", foreignBase, nil, 200, "InventoryResponse")
		if len(foreignRead["items"].([]any)) != 0 || len(foreignRead["connections"].([]any)) != 0 {
			t.Fatal("inventory or connection crossed Workspace")
		}
		awsProblem(t, f.admin.Write("POST", foreignBase+"/imports", awsImportBody(uuid.NewString()), 403, "Problem"), "AWS_SCOPE_REJECTED")
		if f.provider.calls.Load() != before || f.counts(t) != [3]int{1, 1, 0} {
			t.Fatal("replay/refusal reached provider or duplicated durable writes")
		}
		portalKey := uuid.NewString()
		out := f.portal.Write("POST", f.portalBase+"/imports", awsImportBody(portalKey), 200, "ReceiptResponse")
		if out["imported"] != float64(2) || f.provider.calls.Load() != before+3 || f.counts(t) != [3]int{2, 2, 0} {
			t.Fatal("Portal owner actual enabled import")
		}
		var adminAudits, portalAudits int
		if err = f.accounts.DB.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE audience='admin'),count(*) FILTER(WHERE audience='portal') FROM audit_events WHERE action='aws.inventory_imported' AND tenant_id=$1 AND workspace_id=$2 AND actor_user_id=$3`, f.auth.Me.Workspace.TenantID, f.auth.Me.Workspace.ID, f.auth.Me.User.ID).Scan(&adminAudits, &portalAudits); err != nil || adminAudits != 1 || portalAudits != 1 {
			t.Fatal("actual actor/Workspace/audience audit binding", err)
		}
		t.Log("AWS_ENABLED_REPLAY: both entrances, exact receipt/replay without provider, body conflict, ungranted Workspace isolation, distinct Portal import and bound audits")
	})
	t.Run("late_page_and_audit_rollback", func(t *testing.T) {
		f := newEnabledAWSHTTP(t)
		_, inventory := f.baseline(t)
		counts := f.counts(t)
		key := uuid.NewString()
		f.provider.scenario.Store(1)
		awsProblem(t, f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 403, "Problem"), "AWS_ACCESS_DENIED")
		if f.provider.calls.Load() != 6 {
			t.Fatal("late page failure did not follow successful STS/first page")
		}
		f.unchanged(t, inventory, counts, key)
		ctx := context.Background()
		if _, err := f.accounts.DB.Exec(ctx, `CREATE FUNCTION reject_enabled_aws_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='aws.inventory_imported' THEN RAISE EXCEPTION 'synthetic enabled AWS audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_enabled_aws_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_enabled_aws_audit()`); err != nil {
			t.Fatal(err)
		}
		f.provider.scenario.Store(2)
		awsProblem(t, f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 503, "Problem"), "STORAGE_UNAVAILABLE")
		if f.provider.calls.Load() != 8 {
			t.Fatal("audit failure did not follow changed complete provider snapshot")
		}
		f.unchanged(t, inventory, counts, key)
		if _, err := f.accounts.DB.Exec(ctx, "DROP TRIGGER reject_enabled_aws_audit ON audit_events; DROP FUNCTION reject_enabled_aws_audit()"); err != nil {
			t.Fatal(err)
		}
		out := f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 200, "ReceiptResponse")
		if out["imported"] != float64(1) || f.provider.calls.Load() != 10 || f.counts(t) != [3]int{2, 2, 0} {
			t.Fatal("explicit same-key proven rollback recovery")
		}
		after := f.admin.Call("GET", f.adminBase, nil, 200, "InventoryResponse")
		missing, present := 0, 0
		for _, item := range after["items"].([]any) {
			if item.(map[string]any)["missing"] == true {
				missing++
			} else {
				present++
			}
		}
		if missing != 2 || present != 1 {
			t.Fatal("complete new observation did not preserve missing prior resources")
		}
		t.Log("AWS_ENABLED_ROLLBACK: late page/audit failure preserve all durable state; explicit same key recovery persists one current/two missing observations, receipt2/audit2")
	})
	t.Run("certificate_refusal_and_recovery", func(t *testing.T) {
		f := newEnabledAWSHTTP(t)
		_, inventory := f.baseline(t)
		counts := f.counts(t)
		key := uuid.NewString()
		for _, mode := range []int32{1, 2} {
			f.provider.certificateMode.Store(mode)
			awsProblem(t, f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 503, "Problem"), "AWS_UNAVAILABLE")
			if f.provider.calls.Load() != 3 {
				t.Fatal("invalid certificate reached protocol HTTP handler")
			}
			f.unchanged(t, inventory, counts, key)
		}
		f.provider.certificateMode.Store(0)
		out := f.admin.Write("POST", f.adminBase+"/imports", awsImportBody(key), 200, "ReceiptResponse")
		if out["imported"] != float64(2) || f.provider.calls.Load() != 6 || f.counts(t) != [3]int{2, 2, 0} {
			t.Fatal("explicit recovery after certificate repair")
		}
		t.Log("AWS_ENABLED_CERTIFICATES: trusted-CA wrong SAN and correct-SAN untrusted CA refused before HTTP; valid certificate explicit same-key recovery")
	})
}
