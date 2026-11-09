//go:build !linux && !darwin

package main

import (
	"fmt"
	"io"
)

func runSIEM(_ []string, _, stderr io.Writer) int {
	fmt.Fprintln(stderr, "AUDIT_SIEM_PLATFORM_UNSUPPORTED")
	return 2
}
