package factory

import (
	"bytes"
	"debug/elf"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// AgentProfile identifies the dedicated offline Linux amd64 host Agent inventory.
const AgentProfile = "ownsolid-agent-linux-amd64"

const (
	// AgentBinaryAsset identifies the static Linux amd64 Agent executable.
	AgentBinaryAsset = "agent-binary"
	// AgentUnitAsset identifies the host systemd unit shipped with the executable.
	AgentUnitAsset = "agent-unit"
)

const (
	maxAgentBinaryBytes = 64 << 20
	maxAgentUnitBytes   = 32 << 10
)

// WithAgentProfile selects the dedicated Agent inventory for a host install.
func WithAgentProfile(c Config) Config {
	c.current, c.agent = true, true
	return c
}

func (c Config) validateAgentProfile(m wire.Manifest) error {
	if c.RequireRKE2 || c.RequireRKE2Components {
		return fail("RELEASE_RKE2_CURRENT_PROFILE_REQUIRED")
	}
	if m.Profile != AgentProfile {
		return fail("RELEASE_AGENT_PROFILE_REQUIRED")
	}
	if m.SchemaVersion != 1 || m.MinimumPredecessor != 1 || m.MaximumPredecessor != 1 {
		return fail("RELEASE_MANIFEST_INVALID")
	}
	return nil
}

func (c Config) validateAgentAsset(a wire.Asset) error {
	if a.ImageID != "" || a.ImageConfigDigest != "" || (a.ID != AgentBinaryAsset && a.Architecture != "") {
		return fail("RELEASE_ASSET_IMAGE_FIELDS_INVALID")
	}
	switch a.ID {
	case AgentBinaryAsset:
		if a.Kind == "operator" && a.Architecture == "amd64" && a.Size <= maxAgentBinaryBytes {
			return nil
		}
	case AgentUnitAsset:
		if a.Kind == "operator" && a.Size <= maxAgentUnitBytes {
			return nil
		}
	case currentSBOM:
		if a.Kind == "sbom" && a.Size <= c.MaximumMetadataBytes {
			return nil
		}
	default:
		if a.Kind == "license" {
			return nil
		}
	}
	return fail("RELEASE_AGENT_ASSET_INVALID")
}

func checkAgentBinary(raw []byte) error {
	invalid := fail("RELEASE_AGENT_EXECUTABLE_INVALID")
	file, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		return invalid
	}
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != elf.EM_X86_64 ||
		(file.Type != elf.ET_EXEC && file.Type != elf.ET_DYN) ||
		(file.OSABI != elf.ELFOSABI_NONE && file.OSABI != elf.ELFOSABI_LINUX) || file.Entry == 0 {
		return invalid
	}
	executableEntry := false
	for _, segment := range file.Progs {
		if segment.Type == elf.PT_INTERP {
			return invalid
		}
		if segment.Type == elf.PT_LOAD && segment.Flags&elf.PF_X != 0 && file.Entry >= segment.Vaddr &&
			file.Entry-segment.Vaddr < segment.Filesz && segment.Off <= uint64(len(raw)) &&
			segment.Filesz <= uint64(len(raw))-segment.Off {
			executableEntry = true
		}
	}
	if !executableEntry {
		return invalid
	}
	return nil
}
