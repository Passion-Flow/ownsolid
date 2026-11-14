package main

import (
	"io"
	"strings"
	"testing"
)

func TestEncryptionFlagsCannotSelectPlaintext(t *testing.T) {
	for _, args := range [][]string{
		{"backup", "--output", "/backup", "--recipient-file="},
		{"restore", "--from", "/backup", "--identity-file="},
		{"backup", "--output", "/backup", "--identity-file", "/key"},
		{"restore", "--from", "/backup", "--recipient-file", "/key"},
	} {
		err := run(append(args, "--config", "/nonexistent"), io.Discard)
		if err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Fatalf("encryption flags reached configuration loading: %v", err)
		}
	}
}
