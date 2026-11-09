package install

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"go.yaml.in/yaml/v3"
)

const (
	catalogComposeArtifact = "compose.yaml"
	maxCatalogComposeBytes = 1 << 20
	maxCatalogServices     = 256
)

var (
	catalogServiceName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	catalogEnvironment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	catalogProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	errCatalogCompose  = errors.New("INSTALL_CATALOG_COMPOSE_INPUT_REFUSED: use a self-contained Linux amd64 model with no external inputs")
	errCatalogModel    = errors.New("INSTALL_CATALOG_COMPOSE_MODEL_MISMATCH: render and admit the exact staged Compose model")
	errCatalogSource   = errors.New("INSTALL_CATALOG_COMPOSE_SOURCE_MISMATCH: stage the selected signed Compose source again")
)

type catalogComposeModel struct {
	Name     string                    `yaml:"name" json:"name"`
	Services map[string]catalogService `yaml:"services" json:"services"`
}

type catalogService struct {
	Image       string            `yaml:"image" json:"image"`
	PullPolicy  string            `yaml:"pull_policy" json:"pull_policy"`
	NetworkMode string            `yaml:"network_mode" json:"network_mode"`
	Platform    string            `yaml:"platform" json:"platform"`
	Command     []string          `yaml:"command" json:"command"`
	Entrypoint  []string          `yaml:"entrypoint" json:"entrypoint"`
	Environment map[string]string `yaml:"environment" json:"environment"`
	ReadOnly    bool              `yaml:"read_only" json:"read_only"`
}

func catalogSHA256(raw []byte) string { return "sha256:" + wire.Digest(raw) }

func catalogDigest(value string) bool {
	sum, ok := strings.CutPrefix(value, "sha256:")
	return ok && wire.ValidSHA256(sum)
}

// The renderer consumes signed YAML, but only after this closed input check.
// Excluding YAML graph and tag features keeps path-bearing fields explicit.
func readCatalogComposeSource(raw []byte) (catalogComposeModel, error) {
	var source catalogComposeModel
	if !utf8.Valid(raw) {
		return source, errCatalogCompose
	}
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&node) != nil || decoder.Decode(&yaml.Node{}) != io.EOF || !catalogLiteralYAML(&node, 0) {
		return source, errCatalogCompose
	}
	decoder = yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if decoder.Decode(&source) != nil || source.Name != "" || !validCatalogServices(source.Services) {
		return source, errCatalogCompose
	}
	return source, nil
}

func catalogLiteralYAML(node *yaml.Node, depth int) bool {
	if depth > 8 || node.Anchor != "" || node.Style&yaml.TaggedStyle != 0 {
		return false
	}
	switch node.Kind {
	case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode:
		for _, child := range node.Content {
			if !catalogLiteralYAML(child, depth+1) {
				return false
			}
		}
		return true
	case yaml.ScalarNode:
		if node.Tag == "!!bool" {
			return node.Value == "true" || node.Value == "false"
		}
		return node.Tag == "!!str" && catalogLiteral(node.Value)
	default:
		return false
	}
}

func catalogLiteral(value string) bool {
	return !strings.ContainsAny(value, "$\x00")
}

func validCatalogServices(services map[string]catalogService) bool {
	if len(services) == 0 || len(services) > maxCatalogServices {
		return false
	}
	for name, service := range services {
		if !catalogServiceName.MatchString(name) || !catalogDigest(service.Image) ||
			service.PullPolicy != "never" || service.NetworkMode != "none" || service.Platform != "linux/amd64" {
			return false
		}
		for _, args := range [][]string{service.Entrypoint, service.Command} {
			for _, arg := range args {
				if !catalogLiteral(arg) {
					return false
				}
			}
		}
		for key, value := range service.Environment {
			if !catalogEnvironment.MatchString(key) || !catalogLiteral(value) {
				return false
			}
		}
	}
	return true
}
