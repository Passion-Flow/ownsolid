// Command mysqlreference canonicalizes actual first-party measured DDL metadata.
// It has no database connection,SQL execution,credential or network input.
package main

import (
	"io"
	"os"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
)

const maximumInputBytes = 1 << 20

func main() {
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, maximumInputBytes+1))
	if e != nil || len(raw) > maximumInputBytes {
		os.Stderr.WriteString("MYSQL_REFERENCE_INPUT_INVALID\n")
		os.Exit(1)
	}
	canonical, e := mysqlmigrate.CanonicalizeMeasuredEvolution(raw)
	if e != nil {
		os.Stderr.WriteString("MYSQL_REFERENCE_METADATA_INVALID\n")
		os.Exit(1)
	}
	if _, e = os.Stdout.Write(canonical); e != nil {
		os.Exit(1)
	}
}
