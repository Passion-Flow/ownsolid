package v1

import (
	"encoding/json"
	"os"
	"testing"
)

func TestApprovedMaintenanceCatalogAndLegacyRecipeAgree(t *testing.T) {
	current, ok := RecipeForVersion(NodeExporterVersion)
	if !ok || current.ArchiveSHA256 != NodeExporterSHA256 || current.ArchiveBytes != NodeExporterArchiveBytes {
		t.Fatal("legacy/current compiled recipe drift")
	}
	previous, ok := RecipeForVersion("1.12.0")
	if !ok || previous.ArchiveBytes != 12166227 || previous.ArchiveSHA256 != "b206d98e98a431f2687d4c217abb4b15c46a6b272896113ce9a1585cf1a75d1f" {
		t.Fatal("approved previous recipe missing")
	}
	if !IsForwardUpgrade("1.12.0", "1.12.1") || IsForwardUpgrade("1.12.1", "1.12.0") || IsForwardUpgrade("1.12.1", "1.12.1") || IsForwardUpgrade("unapproved", "1.12.1") {
		t.Fatal("catalog invented a forward upgrade")
	}
	if _, ok = RecipeForVersion("https://untrusted.invalid/archive"); ok {
		t.Fatal("arbitrary asset accepted")
	}
	b, e := os.ReadFile("../../../deploy/components/node-exporter/recipe.json")
	if e != nil {
		t.Fatal(e)
	}
	var legacy Recipe
	if json.Unmarshal(b, &legacy) != nil || legacy.Version != current.Version || legacy.ArchiveSHA256 != current.ArchiveSHA256 || legacy.ArchiveBytes != current.ArchiveBytes {
		t.Fatal("legacy deployment recipe drift")
	}
}
func TestBoundedMaintenanceConfigCannotSupplyArguments(t *testing.T) {
	if e := DefaultComponentConfig().Validate(); e != nil {
		t.Fatal(e)
	}
	for _, c := range []ComponentConfig{{LogLevel: "info", MaxScrapeRequests: 0}, {LogLevel: "debug", MaxScrapeRequests: 41}, {LogLevel: "info --web.listen-address=0.0.0.0:9100", MaxScrapeRequests: 1}, {LogLevel: "\n", MaxScrapeRequests: 1}} {
		if c.Validate() == nil {
			t.Fatal("unsafe or unbounded maintenance config accepted")
		}
	}
}

func TestMaintenanceAuthorityCannotEscalateLegacyProfiles(t *testing.T) {
	for _, profile := range []string{ReadOnlyProfile, SystemdComponentsProfile, SystemdMaintenanceProfile} {
		h := Handshake{ProtocolVersion: ProfileVersion(profile), ExecutionProfile: profile, Capabilities: Capabilities(profile), Facts: validFacts()}
		if e := h.Validate(); e != nil {
			t.Fatal(profile, e)
		}
		want := 1
		if profile == SystemdComponentsProfile {
			want = 6
		}
		if profile == SystemdMaintenanceProfile {
			want = 11
		}
		if len(h.Capabilities) != want {
			t.Fatal("authority catalog drift", profile)
		}
		if OperationAllowed(profile, ComponentOperation("upgrade")) != (profile == SystemdMaintenanceProfile) {
			t.Fatal("legacy maintenance escalation")
		}
		h.Capabilities = append(h.Capabilities, "shell.v1")
		if h.Validate() == nil {
			t.Fatal("unknown capability accepted")
		}
	}
	h := Handshake{ProtocolVersion: MaintenanceVersion, ExecutionProfile: SystemdMaintenanceProfile, Capabilities: Capabilities(SystemdComponentsProfile), Facts: validFacts()}
	if h.Validate() == nil {
		t.Fatal("incomplete maintenance handshake accepted")
	}
	h.Capabilities = Capabilities(SystemdMaintenanceProfile)
	h.ProtocolVersion = ManagementVersion
	if h.Validate() == nil {
		t.Fatal("maintenance profile negotiated legacy protocol")
	}
	configuration := DefaultComponentConfig()
	command, e := MaintenanceCommand("configure", NodeExporterVersion, NodeExporterVersion, &configuration)
	if e != nil {
		t.Fatal(e)
	}
	if command.Validate(ComponentOperation("configure")) == nil {
		t.Fatal("legacy command accepted maintenance fields")
	}
}
