package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestAWSTypedConfiguration(t *testing.T) {
	if (AWSConfiguration{}).Validate() == nil {
		t.Fatal("explicit zero policy accepted")
	}
	raw := workspaceConfigInput(t)
	c, e := Decode(raw)
	if e != nil || c.AWS.Enabled || c.AWS.RequestSeconds != 10 {
		t.Fatal("disabled owned defaults", e)
	}
	for _, bad := range []string{"null", "{enabled: 'false'}", "{enabled: false, unknown: secret-sentinel}", "{enabled: false, page_size: 1}", "{enabled: false, enabled: true}", "{connections: null}"} {
		_, e = Decode(append(append([]byte{}, raw...), []byte("aws: "+bad+"\n")...))
		if e == nil || strings.Contains(e.Error(), "secret-sentinel") {
			t.Fatal("invalid AWS configuration accepted", bad, e)
		}
	}
	p := DefaultAWSConfiguration()
	p.Enabled = true
	p.Connections = []AWSConnection{{Name: "fixture", Partition: "aws", AccountID: "123456789012", Regions: []string{"us-east-1"}, WorkspaceIDs: []string{"11111111-1111-4111-8111-111111111111"}, CredentialsFile: "/run/projectone/credentials.json"}}
	if p.Validate() != nil {
		t.Fatal("valid connection")
	}
	p.Connections[0].Regions = []string{"cn-north-1"}
	if p.Validate() == nil {
		t.Fatal("partition escaped")
	}
	p.Connections[0].Regions = []string{"us-east-1"}
	p.Connections[0].WorkspaceIDs = []string{"client-selected"}
	if p.Validate() == nil {
		t.Fatal("invalid scope")
	}
}
func TestAWSPrivateEnvironmentMapping(t *testing.T) {
	raw := workspaceConfigInput(t)
	path := workspaceEnvironmentFixture(t, "PROJECT_ONE_AWS_ENABLED=false\nPROJECT_ONE_AWS_PAGE_SIZE=7\nPROJECT_ONE_AWS_RETRY_ATTEMPTS=1\nPROJECT_ONE_AWS_MAXIMUM_CONCURRENT_IMPORTS=2\n")
	c, e := decodeWithEnvironment(raw, path)
	if e != nil || c.AWS.Enabled || c.AWS.PageSize != 7 || c.AWS.RetryAttempts != 1 || c.AWS.MaximumConcurrentImports != 2 {
		t.Fatal("explicit false and nondefault fields", e)
	}
	for _, v := range []string{"PROJECT_ONE_AWS_ENABLED=False", "PROJECT_ONE_AWS_ENABLED=1", "PROJECT_ONE_AWS_ENABLED=", "PROJECT_ONE_AWS_UNKNOWN=10", "PROJECT_ONE_AWS_PAGE_SIZE=4", "PROJECT_ONE_AWS_PAGE_SIZE=10\nPROJECT_ONE_AWS_PAGE_SIZE=11", "PROJECT_ONE_AWS_RETRY_ATTEMPTS=-1", "PROJECT_ONE_AWS_MAXIMUM_CONCURRENT_IMPORTS=0", "PROJECT_ONE_AWS_MAXIMUM_CONCURRENT_IMPORTS=9"} {
		_, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, v))
		if e == nil {
			t.Fatal("invalid environment accepted", v)
		}
	}
	connections := []AWSConnection{{Name: "fixture", Partition: "aws", AccountID: "123456789012", Regions: []string{"us-east-1"}, WorkspaceIDs: []string{"11111111-1111-4111-8111-111111111111"}, CredentialsFile: "/run/projectone/credentials.json"}}
	b, _ := json.Marshal(connections)
	source := filepath.Join(filepath.Dir(path), "connections.json")
	if os.WriteFile(source, b, 0600) != nil {
		t.Fatal("fixture")
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_AWS_CONNECTIONS_FILE="+source+"\nPROJECT_ONE_AWS_ENABLED=true\nPROJECT_ONE_HTTP_REQUEST_SECONDS=30\n"))
	if e != nil || !c.AWS.Enabled || len(c.AWS.Connections) != 1 {
		t.Fatal("private connection mapping", e)
	}
	encoded, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Decode(encoded); e != nil {
		t.Fatal("typed YAML round trip", e)
	}
}
