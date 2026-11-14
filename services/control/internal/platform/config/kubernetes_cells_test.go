package config

import "testing"

func TestKubernetesCellsConfiguration(t *testing.T) {
	good := KubernetesCellsConfiguration{ArtifactDirectory: "/var/lib/ownsolid/rke2", JoinTokenKeyFile: "/etc/ownsolid/secrets/cell-join-token.key"}.WithDefaults()
	if e := good.Validate(); e != nil {
		t.Fatal(e)
	}
	if good.JoinSeconds != 1800 || good.TokenTTLSeconds != 3600 || good.JoinBatch != 50 || good.ReconcileSeconds != 10 {
		t.Fatalf("defaults %+v", good)
	}
	for name, change := range map[string]func(*KubernetesCellsConfiguration){
		"relative directory":         func(c *KubernetesCellsConfiguration) { c.ArtifactDirectory = "rke2" },
		"unclean directory":          func(c *KubernetesCellsConfiguration) { c.ArtifactDirectory = "/var/lib/../rke2" },
		"no directory":               func(c *KubernetesCellsConfiguration) { c.ArtifactDirectory = "" },
		"no key file":                func(c *KubernetesCellsConfiguration) { c.JoinTokenKeyFile = "" },
		"relative key file":          func(c *KubernetesCellsConfiguration) { c.JoinTokenKeyFile = "key" },
		"join too short":             func(c *KubernetesCellsConfiguration) { c.JoinSeconds = 299 },
		"join too long":              func(c *KubernetesCellsConfiguration) { c.JoinSeconds = 7201 },
		"token shorter than a join":  func(c *KubernetesCellsConfiguration) { c.TokenTTLSeconds = 1800 },
		"token beyond the protocol":  func(c *KubernetesCellsConfiguration) { c.TokenTTLSeconds = 86401 },
		"batch above the guidance":   func(c *KubernetesCellsConfiguration) { c.JoinBatch = 51 },
		"negative batch":             func(c *KubernetesCellsConfiguration) { c.JoinBatch = -1 },
		"reconcile beyond the bound": func(c *KubernetesCellsConfiguration) { c.ReconcileSeconds = 301 },
	} {
		bad := good
		change(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
