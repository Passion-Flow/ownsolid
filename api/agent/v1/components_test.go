package v1

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func validFacts() Facts {
	return Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-contract-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 1024, DiskAvailableBytes: 512, ObservedAt: time.Now().UTC(), AgentVersion: AgentVersion}
}
func TestLegacyProfileShapeAndExplicitManagementCapabilities(t *testing.T) {
	legacy := Handshake{ProtocolVersion: Version, Capabilities: Capabilities(ReadOnlyProfile), Facts: validFacts()}
	if e := legacy.Validate(); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(legacy)
	if strings.Contains(string(b), "execution_profile") || strings.Contains(string(b), "component\"") {
		t.Fatal("legacy handshake shape changed")
	}
	management := Handshake{ProtocolVersion: ManagementVersion, ExecutionProfile: SystemdComponentsProfile, Capabilities: Capabilities(SystemdComponentsProfile), Facts: validFacts()}
	if e := management.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []Handshake{{ProtocolVersion: ManagementVersion, Capabilities: management.Capabilities, Facts: validFacts()}, {ProtocolVersion: Version, ExecutionProfile: SystemdComponentsProfile, Capabilities: management.Capabilities, Facts: validFacts()}, {ProtocolVersion: ManagementVersion, ExecutionProfile: SystemdComponentsProfile, Capabilities: []string{CollectFacts, "shell.v1"}, Facts: validFacts()}} {
		if bad.Validate() == nil {
			t.Fatal("implicit or unknown management authority accepted")
		}
	}
}
func TestRecipeAndMetricsRejectArbitraryEffectsAndFabricatedValues(t *testing.T) {
	command := Command("install")
	if e := command.Validate(ComponentOperation("install")); e != nil {
		t.Fatal(e)
	}
	command.ArtifactSHA256 = strings.Repeat("0", 64)
	if command.Validate(ComponentOperation("install")) == nil {
		t.Fatal("unapproved artifact accepted")
	}
	if IsComponentOperation("run_shell.v1") {
		t.Fatal("arbitrary command catalog entry")
	}
	m := ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 1024, FilesystemAvailableBytes: 512}
	if e := m.Validate(); e != nil {
		t.Fatal(e)
	}
	m.MemoryAvailableBytes = 2048
	if m.Validate() == nil {
		t.Fatal("impossible memory accepted")
	}
	m.MemoryAvailableBytes = 512
	m.CPUSecondsTotal = math.NaN()
	if m.Validate() == nil {
		t.Fatal("NaN metrics accepted")
	}
	o := ComponentObservation{Component: NodeExporter, Version: NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE"}
	if e := o.Validate(); e != nil {
		t.Fatal(e)
	}
	o.Metrics = &m
	if o.Validate() == nil {
		t.Fatal("unknown service published numeric metrics")
	}
}
