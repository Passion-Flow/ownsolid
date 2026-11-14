package imageregistry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRegistryNodeTypedConfiguration(t *testing.T) {
	root := t.TempDir()
	c := Defaults()
	c.Root = filepath.Join(root, "imports")
	c.DockerDataRoot = filepath.Join(root, "actual-docker-data")
	c.Verification.MountRoot = root
	c.Verification.TrustFile = filepath.Join(root, "manufacturer-trust.json")
	c.OperationSeconds = 7199
	c.MaximumDockerResponseBytes = 2 << 20
	file := filepath.Join(root, "configuration.json")
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	actual, e := LoadEnvironment(map[string]string{"PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE": file})
	if e != nil || !reflect.DeepEqual(actual, c) || actual.Verification.AllowTestFixture {
		t.Fatal("typed nondefault/false contract changed", e)
	}
	for _, values := range []map[string]string{nil, {}, {"PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE": ""}, {"PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE": file, "UNKNOWN": "false"}} {
		if _, e := LoadEnvironment(values); e == nil {
			t.Fatal("ambiguous node environment accepted")
		}
	}
	for _, bad := range []string{`{"root":null}`, `{"root":"/one","root":"/two"}`, `{"skip_tls":false}`, `{"operation_seconds":0}`} {
		if e = os.WriteFile(file, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = Load(file); e == nil {
			t.Fatal("invalid configuration accepted", bad)
		}
	}
	for _, kind := range []string{"fixture", "metadata-cap", "trust", "headroom", "deadline"} {
		bad := c
		switch kind {
		case "fixture":
			bad.Verification.AllowTestFixture = true
		case "metadata-cap":
			bad.MaximumDockerResponseBytes = bad.Verification.MaximumMetadataBytes + 1
		case "trust":
			bad.Verification.TrustFile = ""
		case "headroom":
			bad.MinimumHeadroomPercent = 29
		case "deadline":
			bad.OperationSeconds = 86401
		}
		if bad.Validate() == nil {
			t.Fatal("invalid node admission accepted", kind)
		}
	}
}
