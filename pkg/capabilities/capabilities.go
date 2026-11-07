// Package capabilities is the one declaration of every option the product offers and
// whether this release can do it (ADR 0036, ADR 0042). The installer, the control plane,
// the API and both entrances read it, so what they say cannot differ.
package capabilities

import (
	"bytes"
	_ "embed" // Holds the declaration inside every program that reads it.
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

// State says whether an option can be chosen in this release.
type State string

const (
	// Ready is implemented and usable now.
	Ready State = "ready"
	// Planned will be supported; the roadmap item behind it is open.
	Planned State = "planned"
	// Licensed works under a licence or an account the customer holds.
	Licensed State = "licensed"
	// Unsupported is a decision not to support the option, with the reasoning.
	Unsupported State = "unsupported"
)

// InstallSizes names the registry the installer and the control plane read their sizes from.
const InstallSizes = "install-sizes"

// Option is one choice in a registry.
type Option struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State State  `json:"state"`
	// RoadmapItem makes the option ready or, once ready, is what made it so.
	RoadmapItem string `json:"roadmap_item,omitempty"`
	// Reason says why the option cannot simply be chosen. Every state but ready has one.
	Reason string `json:"reason,omitempty"`
	// Note adds a fact about what is ready, such as what it was run on.
	Note string `json:"note,omitempty"`
}

// Registry is one family of options, such as the accelerators.
type Registry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Options     []Option `json:"options"`
}

// Lookup finds an option by its ID.
func (r Registry) Lookup(id string) (Option, bool) {
	for _, option := range r.Options {
		if option.ID == id {
			return option, true
		}
	}
	return Option{}, false
}

const formatVersion = 1

//go:embed declaration.json
var embedded []byte

// A declaration that does not parse is a build defect, and it stops every program and test at once.
var declared = mustParse(embedded)

func mustParse(raw []byte) []Registry {
	registries, e := parse(raw)
	if e != nil {
		panic(e)
	}
	return registries
}

// Registries returns every registry in display order. The caller owns the copy.
func Registries() []Registry {
	out := make([]Registry, len(declared))
	for i, registry := range declared {
		registry.Options = append([]Option(nil), registry.Options...)
		out[i] = registry
	}
	return out
}

// Lookup finds one registry by its ID. The caller owns the copy.
func Lookup(id string) (Registry, bool) {
	for _, registry := range Registries() {
		if registry.ID == id {
			return registry, true
		}
	}
	return Registry{}, false
}

var (
	identifier = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	item       = regexp.MustCompile(`^[A-Z]{2,4}-[0-9]{3}$`)
)

func parse(raw []byte) ([]Registry, error) {
	var document struct {
		FormatVersion int        `json:"format_version"`
		Registries    []Registry `json:"registries"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&document); e != nil {
		return nil, fmt.Errorf("capability declaration: %w", e)
	}
	if _, e := decoder.Token(); e != io.EOF {
		return nil, fmt.Errorf("capability declaration: trailing content")
	}
	if document.FormatVersion != formatVersion {
		return nil, fmt.Errorf("capability declaration: format_version must be %d", formatVersion)
	}
	if len(document.Registries) == 0 {
		return nil, fmt.Errorf("capability declaration: no registries")
	}
	seen := map[string]bool{}
	for _, registry := range document.Registries {
		if e := validateRegistry(registry); e != nil {
			return nil, fmt.Errorf("capability declaration: %w", e)
		}
		if seen[registry.ID] {
			return nil, fmt.Errorf("capability declaration: registry %s is declared twice", registry.ID)
		}
		seen[registry.ID] = true
	}
	return document.Registries, nil
}

func validateRegistry(registry Registry) error {
	if !identifier.MatchString(registry.ID) {
		return fmt.Errorf("registry ID %q is not kebab-case", registry.ID)
	}
	if registry.Name == "" || registry.Description == "" {
		return fmt.Errorf("registry %s needs a name and a description", registry.ID)
	}
	if len(registry.Options) == 0 {
		return fmt.Errorf("registry %s has no options", registry.ID)
	}
	seen := map[string]bool{}
	for _, option := range registry.Options {
		if e := validateOption(option); e != nil {
			return fmt.Errorf("%s: %w", registry.ID, e)
		}
		if seen[option.ID] {
			return fmt.Errorf("%s: option %s is declared twice", registry.ID, option.ID)
		}
		seen[option.ID] = true
	}
	return nil
}

func validateOption(option Option) error {
	if !identifier.MatchString(option.ID) {
		return fmt.Errorf("option ID %q is not kebab-case", option.ID)
	}
	if option.Name == "" {
		return fmt.Errorf("%s has no name", option.ID)
	}
	if option.RoadmapItem != "" && !item.MatchString(option.RoadmapItem) {
		return fmt.Errorf("%s: %q is not a roadmap item", option.ID, option.RoadmapItem)
	}
	switch option.State {
	case Ready:
		if option.RoadmapItem == "" {
			return fmt.Errorf("%s is ready and names no roadmap item", option.ID)
		}
		if option.Reason != "" {
			return fmt.Errorf("%s is ready and gives a reason", option.ID)
		}
	case Planned, Licensed:
		if option.RoadmapItem == "" || option.Reason == "" {
			return fmt.Errorf("%s is %s and needs a roadmap item and a reason", option.ID, option.State)
		}
	case Unsupported:
		if option.Reason == "" {
			return fmt.Errorf("%s is unsupported and gives no reason", option.ID)
		}
		if option.RoadmapItem != "" {
			return fmt.Errorf("%s is unsupported and names a roadmap item", option.ID)
		}
	default:
		return fmt.Errorf("%s has unknown state %q", option.ID, option.State)
	}
	return nil
}
