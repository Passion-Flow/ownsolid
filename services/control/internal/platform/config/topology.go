package config

import (
	"errors"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
	"go.yaml.in/yaml/v3"
)

// TopologySize is the one deployment model that replaces the former
// application profiles. Every size runs the complete platform; a size only
// decides how many replicas, cells and sites carry it.
type TopologySize string

const (
	TopologySingleNode TopologySize = "single-node"
	TopologyHA         TopologySize = "ha"
	TopologyCell       TopologySize = "cell"
	TopologyMultiCell  TopologySize = "multi-cell"
	TopologyMultiSite  TopologySize = "multi-site"
)

// TopologySizes lists every size in increasing scale.
var TopologySizes = []TopologySize{TopologySingleNode, TopologyHA, TopologyCell, TopologyMultiCell, TopologyMultiSite}

// Known reports whether the size is part of the topology model.
func (s TopologySize) Known() bool {
	for _, known := range TopologySizes {
		if s == known {
			return true
		}
	}
	return false
}

// Supported reports whether this release can run the size, from the capability declaration.
// A size becomes ready only when its roadmap item is implemented and tested; selecting a known
// but unsupported size fails at startup instead of running a different shape than the operator
// asked for.
func (s TopologySize) Supported() bool {
	sizes, found := capabilities.Lookup(capabilities.InstallSizes)
	if !found {
		return false
	}
	option, found := sizes.Lookup(string(s))
	return found && option.State == capabilities.Ready
}

// TopologyConfiguration selects the deployment size. Omitting the group or
// the size selects single-node.
type TopologyConfiguration struct {
	Size TopologySize `yaml:"size,omitempty" json:"size"`
}

// EffectiveSize returns the configured size, or single-node when omitted.
func (t TopologyConfiguration) EffectiveSize() TopologySize {
	if t.Size == "" {
		return TopologySingleNode
	}
	return t.Size
}

// Validate rejects unknown sizes and sizes this release cannot run.
func (t TopologyConfiguration) Validate() error {
	size := t.EffectiveSize()
	if !size.Known() {
		return errors.New("CONFIG_TOPOLOGY_SIZE_INVALID")
	}
	if !size.Supported() {
		return errors.New("CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED")
	}
	return nil
}

func (t *TopologyConfiguration) UnmarshalYAML(node *yaml.Node) error {
	bad := errors.New("CONFIG_TOPOLOGY_INVALID")
	if node.Kind != yaml.MappingNode {
		return bad
	}
	var value TopologyConfiguration
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, field := node.Content[i], node.Content[i+1]
		if key.Tag != "!!str" || key.Value != "size" || value.Size != "" {
			return bad
		}
		if field.Kind != yaml.ScalarNode || field.Tag != "!!str" || field.Value == "" {
			return errors.New("CONFIG_TOPOLOGY_SIZE_INVALID")
		}
		value.Size = TopologySize(field.Value)
	}
	*t = value
	return nil
}

// validateTopologyYAML rejects an explicit null topology group, which the YAML
// library would otherwise decode as omission.
func validateTopologyYAML(raw []byte) error {
	var node yaml.Node
	if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := node.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "topology" && root.Content[i+1].Kind != yaml.MappingNode {
			return errors.New("CONFIG_TOPOLOGY_INVALID")
		}
	}
	return nil
}
