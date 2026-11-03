package engine

import (
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"testing"
)

func TestLocalPublisherPlatformAndExactProviderRefusal(t *testing.T) {
	var p Policy
	if wire.StrictJSON(wire.EngineProfile, &p) != nil {
		t.Fatal("invalid pinned Engine profile")
	}
	for _, v := range []struct {
		os, arch string
		allowed  bool
	}{{"darwin", "arm64", true}, {"darwin", "amd64", true}, {"linux", "amd64", true}, {"linux", "arm64", true}, {"linux", "riscv64", false}, {"windows", "arm64", false}, {"windows", "amd64", false}, {"linux", "386", false}} {
		if localOperatorPlatform(p, v.os, v.arch) != v.allowed {
			t.Fatal("operator platform admission", v)
		}
	}
	observed := Observation{EngineSHA256: wire.Digest([]byte("synthetic-engine")), HostArchitecture: "arm64", EngineVersion: "29.8.2"}
	info := infoReply{ID: "synthetic-engine", OperatingSystem: "Docker Desktop", OSType: "linux", ServerVersion: "29.8.2", Architecture: "aarch64"}
	if !localPublisherEngine(info, observed) {
		t.Fatal("explicit local container provider refused")
	}
	for _, change := range []func(*infoReply){func(v *infoReply) { v.ID = "other-engine" }, func(v *infoReply) { v.OperatingSystem = "Ubuntu 24.04" }, func(v *infoReply) { v.OSType = "windows" }, func(v *infoReply) { v.ServerVersion = "29.9.0" }, func(v *infoReply) { v.Architecture = "amd64" }} {
		bad := info
		change(&bad)
		if localPublisherEngine(bad, observed) {
			t.Fatal("foreign or changed local provider admitted")
		}
	}
}
