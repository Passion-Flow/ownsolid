package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTrainingConfigurationFile(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	writeFile(t, valid, []byte(" \n{\"device\":\"nvidia_bfloat16\"}\n"), 0600)
	config, err := readTrainingConfiguration(valid)
	if err != nil || string(config) != `{"device":"nvidia_bfloat16"}` {
		t.Fatalf("read training configuration: %v", err)
	}
	for _, tt := range []struct{ name, content, want string }{
		{"array", "[]", "one JSON object"},
		{"multiple", "{} {}", "one JSON object"},
		{"invalid_utf8", "{\"device\":\"\xff\"}", "one JSON object"},
		{"oversize", strings.Repeat(" ", 64<<10) + "{}", "exceeds 64 KiB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			writeFile(t, path, []byte(tt.content), 0600)
			if _, err := readTrainingConfiguration(path); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want %q, got %v", tt.want, err)
			}
		})
	}
	if _, err := readTrainingConfiguration(dir); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory input: %v", err)
	}
}
