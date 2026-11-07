package install

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
)

var capabilityStates = map[capabilities.State]string{
	capabilities.Ready:       "Available",
	capabilities.Planned:     "Planned",
	capabilities.Licensed:    "Needs your licence",
	capabilities.Unsupported: "Not supported",
}

// WriteCapabilities prints every option of this release, grouped by registry: the same
// declaration the installer checks a configuration against and both entrances show.
func WriteCapabilities(out io.Writer) error {
	for i, registry := range capabilities.Registries() {
		if i > 0 {
			if _, e := fmt.Fprintln(out); e != nil {
				return e
			}
		}
		if _, e := fmt.Fprintf(out, "%s: %s\n", registry.Name, registry.Description); e != nil {
			return e
		}
		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, option := range registry.Options {
			detail := option.Reason
			if detail == "" {
				detail = option.Note
			}
			if _, e := fmt.Fprintf(table, "  %s\t%s\t%s\t%s\t%s\n", option.ID, option.Name, capabilityStates[option.State], option.RoadmapItem, detail); e != nil {
				return e
			}
		}
		if e := table.Flush(); e != nil {
			return e
		}
	}
	return nil
}
