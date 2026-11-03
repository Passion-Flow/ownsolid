// Command private-cluster-probe is the one bounded manufacturer Job program.
package main

import (
	"fmt"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
)

func main() {
	if e := privateclusterproof.Run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "PRIVATE_CLUSTER_PROBE_FAILED")
		os.Exit(1)
	}
}
