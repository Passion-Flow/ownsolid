package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWSCompleteNondefaultEnvironmentAndDisabledCredentialReference(t *testing.T) {
	input := workspaceConfigInput(t)
	values := strings.Join([]string{
		"PROJECT_ONE_AWS_ENABLED=false",
		"PROJECT_ONE_AWS_MAXIMUM_CONCURRENT_IMPORTS=2",
		"PROJECT_ONE_AWS_REQUEST_SECONDS=3",
		"PROJECT_ONE_AWS_IMPORT_SECONDS=17",
		"PROJECT_ONE_AWS_RETRY_ATTEMPTS=2",
		"PROJECT_ONE_AWS_RETRY_MILLISECONDS=4",
		"PROJECT_ONE_AWS_PAGE_SIZE=7",
		"PROJECT_ONE_AWS_MAXIMUM_PAGES=2",
		"PROJECT_ONE_AWS_MAXIMUM_RESOURCES=9",
		"PROJECT_ONE_AWS_MAXIMUM_RECEIPTS=11",
		"PROJECT_ONE_AWS_MAXIMUM_RESPONSE_BYTES=2048",
	}, "\n") + "\n"
	c, e := decodeWithEnvironment(input, workspaceEnvironmentFixture(t, values))
	if e != nil {
		t.Fatal(e)
	}
	a := c.AWS
	if a.Enabled || a.MaximumConcurrentImports != 2 || a.RequestSeconds != 3 || a.ImportSeconds != 17 || a.RetryAttempts != 2 || a.RetryMilliseconds != 4 || a.PageSize != 7 || a.MaximumPages != 2 || a.MaximumResources != 9 || a.MaximumReceipts != 11 || a.MaximumResponseBytes != 2048 {
		t.Fatal("nondefault env projection dropped a policy")
	}
	// Disabled/offline configuration must not open or require provider credentials.
	// Discovery will validate the protected reference when explicitly requested.
	a.Connections = []AWSConnection{{Name: "offline", Partition: "aws", AccountID: "123456789012", Regions: []string{"us-west-2"}, WorkspaceIDs: []string{"11111111-1111-4111-8111-111111111111"}, CredentialsFile: filepath.Join(t.TempDir(), "not-provisioned.json")}}
	if a.Validate() != nil {
		t.Fatal("disabled connection required provider credential work")
	}
	if _, e = os.Stat(a.Connections[0].CredentialsFile); !os.IsNotExist(e) {
		t.Fatal("validation created or opened credentials")
	}
}

func TestAWSInvalidOverridesRejectRatherThanDefault(t *testing.T) {
	for _, value := range []string{
		"PROJECT_ONE_AWS_REQUEST_SECONDS=0", "PROJECT_ONE_AWS_REQUEST_SECONDS=61",
		"PROJECT_ONE_AWS_IMPORT_SECONDS=9", "PROJECT_ONE_AWS_IMPORT_SECONDS=601",
		"PROJECT_ONE_AWS_RETRY_ATTEMPTS=0", "PROJECT_ONE_AWS_RETRY_ATTEMPTS=6",
		"PROJECT_ONE_AWS_RETRY_MILLISECONDS=0", "PROJECT_ONE_AWS_RETRY_MILLISECONDS=5001",
		"PROJECT_ONE_AWS_PAGE_SIZE=1001", "PROJECT_ONE_AWS_MAXIMUM_PAGES=0", "PROJECT_ONE_AWS_MAXIMUM_PAGES=1001",
		"PROJECT_ONE_AWS_MAXIMUM_RESOURCES=0", "PROJECT_ONE_AWS_MAXIMUM_RESOURCES=100001",
		"PROJECT_ONE_AWS_MAXIMUM_RECEIPTS=0", "PROJECT_ONE_AWS_MAXIMUM_RECEIPTS=100001",
		"PROJECT_ONE_AWS_MAXIMUM_RESPONSE_BYTES=1023", "PROJECT_ONE_AWS_MAXIMUM_RESPONSE_BYTES=16777217",
		"PROJECT_ONE_AWS_MAXIMUM_CONCURRENT_IMPORTS=", "PROJECT_ONE_AWS_PAGE_SIZE=7.5", "PROJECT_ONE_AWS_PAGE_SIZE=1e2",
	} {
		t.Run(strings.Split(value, "=")[0]+"_"+strings.Split(value, "=")[1], func(t *testing.T) {
			_, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, value+"\n"))
			if e == nil {
				t.Fatal("invalid override silently became a default")
			}
		})
	}
}

func TestAWSTrackedDeploymentExamplesUseActualPrivateLoader(t *testing.T) {
	policy, e := os.ReadFile("../../../config/examples/aws/policy.env.example")
	if e != nil {
		t.Fatal(e)
	}
	connections, e := os.ReadFile("../../../config/examples/aws/connections.example.json")
	if e != nil {
		t.Fatal(e)
	}
	envFile := workspaceEnvironmentFixture(t, string(policy))
	privateConnections := filepath.Join(filepath.Dir(envFile), "aws-example-connections.json")
	if e = os.WriteFile(privateConnections, connections, 0600); e != nil {
		t.Fatal(e)
	}
	// Keep disabled from the tracked sample: validating a deployment projection
	// must never seek cloud credentials or turn on discovery implicitly.
	policy = append(policy, []byte("\nPROJECT_ONE_AWS_CONNECTIONS_FILE="+privateConnections+"\n")...)
	c, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, string(policy)))
	if e != nil {
		t.Fatal("tracked examples do not load through supported boundary", e)
	}
	if c.AWS.Enabled || len(c.AWS.Connections) == 0 || c.AWS.ImportSeconds > c.Policy().HTTP.RequestSeconds {
		t.Fatal("tracked deployment example lost offline/default budget contract")
	}
}
