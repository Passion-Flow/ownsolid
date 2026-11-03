package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeScopeBindingPreservesOriginalConfigurationBytes(t *testing.T) {
	c := Defaults()
	before, _ := json.Marshal(c)
	fingerprint := c.Fingerprint()
	c.BoundScopeID = strings.Repeat("a", 32)
	after, _ := json.Marshal(c)
	if string(before) != string(after) || c.Fingerprint() != fingerprint {
		t.Fatal("runtime binding changed original persisted authority")
	}
	if _, e := Load(before, []byte("PROJECT_ONE_OFFLINE_OPERATOR_-=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")); e == nil || e.Error() != "OFFLINE_OPERATOR_ENVIRONMENT_INVALID" {
		t.Fatal("hidden JSON tag became env override", e)
	}
	if _, e := Load([]byte(`{"BoundScopeID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil); e == nil {
		t.Fatal("runtime binding accepted from JSON")
	}
}
