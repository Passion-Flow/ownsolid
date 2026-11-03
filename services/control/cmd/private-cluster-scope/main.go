// Command private-cluster-scope renders a reviewed isolated scope; it never
// contacts Kubernetes, writes a token, executes kubectl or applies an object.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters"
)

func main() {
	profile := flag.String("profile", "", "Protected connection profile ID (required)")
	flag.Parse()
	if flag.NArg() != 0 || *profile == "" {
		fmt.Fprintln(os.Stderr, "PRIVATE_CLUSTER_PROFILE_REQUIRED")
		os.Exit(2)
	}
	c, e := privateclusters.LoadEnvironment(map[string]string{privateclusters.ConfigEnvironment: os.Getenv(privateclusters.ConfigEnvironment)})
	if e == nil {
		var objects map[string]any
		objects, e = privateclusters.ScopeObjects(c, *profile)
		if e == nil {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			e = encoder.Encode(objects)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "PRIVATE_CLUSTER_SCOPE_RENDER_FAILED")
		os.Exit(1)
	}
}
