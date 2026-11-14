package bundle

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVerificationExplicitEnvironmentUsesFactoryPolicyAndPublicFields(t *testing.T) {
	t.Setenv("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES", "-1")
	p := Defaults()
	p.AllowTestFixture = true
	p.MinimumFreeBytes = 1 << 50
	raw, _ := json.Marshal(p)
	v, e := LoadVerification(raw, []byte("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=34359738368\nPROJECT_ONE_OFFLINE_FACTORY_MAXIMUM_ASSETS=1024\nPROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=false\n"))
	if e != nil || v.MinimumFreeBytes != 32<<30 || v.MaximumAssets != 1024 || v.AllowTestFixture {
		t.Fatal("explicit public policy precedence/false", e)
	}
	legacy := Defaults()
	raw, _ = json.Marshal(legacy)
	got, e := LoadVerification(raw, nil)
	encoded, _ := json.Marshal(got)
	if e != nil || !bytes.Equal(raw, encoded) {
		t.Fatal("legacy bytes or ambient isolation", e)
	}
	for _, environment := range []string{
		"PROJECT_ONE_OFFLINE_FACTORY_PRIVATE_KEY_FILE=/private/reference",
		"PROJECT_ONE_OFFLINE_FACTORY_OUTPUT_FILE=/private/output",
		"PROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=30",
		"PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=1\nPROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=2",
		"PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=",
		"PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=-1",
		"PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=1125899906842625",
		"PROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=False",
		"PROJECT_ONE_OFFLINE_FACTORY_UNKNOWN=1",
		"PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTE\u017f=1",
		"PROJECT_ONE_OFFLINE_FACTORY_MOUNT_ROOT=\xff",
	} {
		if _, e := LoadVerification(raw, []byte(environment)); e == nil {
			t.Fatal("invalid/nonpublic environment accepted", environment)
		}
	}
}
