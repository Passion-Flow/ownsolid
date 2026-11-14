package privateclusters

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderedScopeCannotGrantProductionMutation(t *testing.T) {
	c := syntheticConfig()
	objects, e := ScopeObjects(c, c.Profiles[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(objects)
	s := string(raw)
	for _, forbidden := range []string{`"resources":["*"]`, `"verbs":["*"]`, `"secrets"`, `"pods/exec"`, `"token"`, `"kind":"Secret"`, `"uid":`, `"namespace":"default"`} {
		if strings.Contains(s, forbidden) {
			t.Fatal("unsafe rendered authority")
		}
	}
	if !strings.Contains(s, `"name":"`+c.Profiles[0].Namespace+`"`) || !strings.Contains(s, `"automountServiceAccountToken":false`) {
		t.Fatal("real isolated scope shape missing")
	}
	if _, e = ScopeObjects(c, "unconfigured"); e == nil {
		t.Fatal("client-selected profile adopted")
	}
}
