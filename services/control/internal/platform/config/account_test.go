package config

import (
	"os"
	"testing"
)

func TestAccountApplicationProfilePolicy(t *testing.T) {
	c := defaultConfiguration()
	if c.AccountOnly() || c.Server.Profile() != "platform" {
		t.Fatal("legacy default changed")
	}
	for _, profile := range []string{"account", "workspaces", "platform"} {
		c.Server.ApplicationProfile = &profile
		if e := c.ValidateApplicationProfile(); e != nil {
			t.Fatal(e)
		}
	}
	for _, profile := range []string{"", "false", "unknown", "ACCOUNT"} {
		c.Server.ApplicationProfile = &profile
		if c.ValidateApplicationProfile() == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	profile := "account"
	c.Server.ApplicationProfile = &profile
	c.SSO.Enabled = true
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("future dependency silently enabled")
	}
	c.SSO.Enabled = false
	c.Database.Engine = MySQLDatabaseEngine
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("unsupported account adapter accepted")
	}
}
func TestAccountProfileStrictYAMLAndEnvironment(t *testing.T) {
	for _, value := range []string{"null", "false", "''", "unknown", "[]"} {
		if validateServerTransportYAML([]byte("server:\n  application_profile: "+value+"\n")) == nil {
			t.Fatal("invalid YAML accepted", value)
		}
	}
	var server ServerConfiguration
	if e := server.applyTransportEnvironment("PROJECT_ONE_APPLICATION_PROFILE", "account"); e != nil || server.Profile() != "account" {
		t.Fatal("explicit profile mapping failed")
	}
	if server.applyTransportEnvironment("PROJECT_ONE_APPLICATION_PROFILE", "") == nil {
		t.Fatal("empty environment accepted")
	}
}

func TestWorkspacesProfileRefusesFutureDependencies(t *testing.T) {
	c := defaultConfiguration()
	profile := "workspaces"
	c.Server.ApplicationProfile = &profile
	if !c.AccountRuntime() || !c.WorkspacesOnly() || c.AccountOnly() {
		t.Fatal("profile projection")
	}
	if e := validateServerTransportYAML([]byte("server:\n  application_profile: workspaces\n")); e != nil {
		t.Fatal(e)
	}
	if e := c.Server.applyTransportEnvironment("PROJECT_ONE_APPLICATION_PROFILE", "workspaces"); e != nil {
		t.Fatal(e)
	}
	c.SSO.Enabled = true
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("SSO dependency accepted")
	}
	c.SSO.Enabled = false
	c.Database.Engine = MySQLDatabaseEngine
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("adapter accepted")
	}
}

func TestNodesProfileAndMachineTransport(t *testing.T) {
	c := defaultConfiguration()
	profile := "nodes"
	c.Server.ApplicationProfile = &profile
	if !c.AccountRuntime() || !c.WorkspaceRuntime() || !c.NodesOnly() || c.WorkspacesOnly() {
		t.Fatal("profile")
	}
	if e := c.ValidateApplicationProfile(); e != nil {
		t.Fatal(e)
	}
	if e := validateServerTransportYAML([]byte("server:\n  application_profile: nodes\n")); e != nil {
		t.Fatal(e)
	}
	c.Server.TransportMode = TransportHTTP
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("machine transport downgraded")
	}
	c.MachineServer = &MachineServerConfiguration{}
	if e := c.ValidateApplicationProfile(); e != nil {
		t.Fatal("dedicated machine composition", e)
	}
	c.SSO.Enabled = true
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("future service enabled")
	}
}

func TestNodesTrackedExamplesAndIndependentMachineOrigin(t *testing.T) {
	for _, name := range []string{"nodes.example.yaml", "nodes-http.example.yaml"} {
		c, e := Load("../../../config/examples/control/" + name)
		if e != nil {
			t.Fatal(name, e)
		}
		if !c.NodesOnly() {
			t.Fatal("profile", name)
		}
		origin, e := c.MachineOrigin()
		if e != nil {
			t.Fatal(name, e)
		}
		if name == "nodes-http.example.yaml" && (c.Server.EffectiveTransportMode() != TransportHTTP || origin != "https://127.0.0.1:9443" || c.Server.AdminOrigin != "http://127.0.0.1:8080") {
			t.Fatal("browser/machine origins rewritten")
		}
	}
}

func TestComponentsProfileAndProtectedArchiveMapping(t *testing.T) {
	for _, name := range []string{"components.example.yaml", "components-http.example.yaml"} {
		c, e := Load("../../../config/examples/control/" + name)
		if e != nil {
			t.Fatal(name, e)
		}
		if !c.ComponentsOnly() || !c.NodeRuntime() || !c.WorkspaceRuntime() || !c.AccountRuntime() || c.NodesOnly() {
			t.Fatal("composition")
		}
		if e = c.ValidateApplicationProfile(); e != nil {
			t.Fatal(e)
		}
		if name == "components-http.example.yaml" {
			origin, e := c.MachineOrigin()
			if e != nil || origin != "https://127.0.0.1:9443" || c.Server.AdminOrigin != "http://127.0.0.1:8080" {
				t.Fatal("independent machine HTTPS", e)
			}
			c.MachineServer = nil
			if c.ValidateApplicationProfile() == nil {
				t.Fatal("machine downgrade")
			}
		}
	}
	raw := workspaceConfigInput(t)
	t.Setenv("PROJECT_ONE_COMPONENT_NODE_EXPORTER_ARCHIVE_FILE", "/ignored")
	c, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_APPLICATION_PROFILE=components\nPROJECT_ONE_COMPONENT_NODE_EXPORTER_ARCHIVE_FILE=/approved/exporter.tar.gz\n"))
	if e != nil || c.Components.NodeExporterArchiveFile != "/approved/exporter.tar.gz" {
		t.Fatal(e)
	}
	c.Components.NodeExporterCatalogDirectory = "/not-m04"
	if c.ValidateApplicationProfile() == nil {
		t.Fatal("maintenance catalog accepted")
	}
	for _, v := range []string{"", "false", "relative", "/a/../b"} {
		if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_COMPONENT_NODE_EXPORTER_ARCHIVE_FILE="+v+"\n")); e == nil {
			t.Fatal("invalid archive path", v)
		}
	}
}

func TestMaintenanceProfileExamplesAndEnvironment(t *testing.T) {
	for _, name := range []string{"maintenance.example.yaml", "maintenance-http.example.yaml"} {
		raw, e := os.ReadFile("../../../config/examples/control/" + name)
		if e != nil {
			t.Fatal(e)
		}
		c, e := Decode(raw)
		if e != nil || !c.MaintenanceOnly() || !c.ComponentRuntime() {
			t.Fatal(name, e)
		}
		c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_APPLICATION_PROFILE=maintenance\nPROJECT_ONE_COMPONENT_NODE_EXPORTER_CATALOG_DIRECTORY=/approved/catalog\n"))
		if e != nil || c.Components.NodeExporterCatalogDirectory != "/approved/catalog" {
			t.Fatal(e)
		}
		for _, v := range []string{"false", "relative", "/a/../b", ""} {
			if _, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "PROJECT_ONE_COMPONENT_NODE_EXPORTER_CATALOG_DIRECTORY="+v+"\n")); e == nil {
				t.Fatal("invalid catalog accepted", v)
			}
		}
	}
}
