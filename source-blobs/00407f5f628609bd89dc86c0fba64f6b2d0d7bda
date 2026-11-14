package main

import (
	"io"
	"strings"
	"testing"
)

func TestClusterSupportFlagIsAnExclusiveInput(t *testing.T) {
	for _, args := range [][]string{
		{"status", "--cluster-config", "/cluster.json"},
		{"support-bundle", "--cluster-config=", "--output", "/bundle.tar.gz"},
		{"support-bundle", "--config=", "--cluster-config", "/cluster.json", "--output", "/bundle.tar.gz"},
		{"support-bundle", "--cluster-config", "/cluster.json"},
	} {
		if err := run(args, io.Discard); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Fatalf("invalid flags reached input loading: %v", err)
		}
	}
	err := run([]string{"support-bundle", "--cluster-config", "/nonexistent/cluster.json", "--output", "/bundle.tar.gz"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "INSTALL_CLUSTER_SUPPORT_INPUT_UNAVAILABLE") {
		t.Fatalf("cluster input did not reach its collector: %v", err)
	}
}
