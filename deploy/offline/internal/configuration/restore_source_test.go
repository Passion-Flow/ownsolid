package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRestoreSourceRequestDefaultsAndExplicitEnvironment(t *testing.T) {
	raw := []byte(`{"backup_root":"/synthetic/external/backup","manifest_sha256":"` + strings.Repeat("a", 64) + `"}`)
	r, e := LoadRestoreSourceRequest(raw, nil)
	if e != nil || r.MaximumBytes != 64<<30 || r.MaximumEntries != 100000 || r.FormatVersion != 1 {
		t.Fatal(r, e)
	}
	env := []byte("# explicit protected source mapping\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT=/synthetic/portable/backup\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_MANIFEST_SHA256=" + strings.Repeat("b", 64) + "\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES=33554432\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES=246\n")
	r, e = LoadRestoreSourceRequest(raw, env)
	if e != nil || r.BackupRoot != "/synthetic/portable/backup" || r.ManifestSHA256 != strings.Repeat("b", 64) || r.MaximumBytes != 32<<20 || r.MaximumEntries != 246 {
		t.Fatal(r, e)
	}
	for _, key := range []string{"PROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MANIFEST_SHA256", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES"} {
		t.Setenv(key, "ignored_ambient_secret_marker")
	}
	if r, e = LoadRestoreSourceRequest(raw, nil); e != nil || r.BackupRoot != "/synthetic/external/backup" {
		t.Fatal("ambient environment consumed", e)
	}
}

func TestRestoreSourceRequestInvalidInputsAndRedaction(t *testing.T) {
	base := RestoreSourceDefaults()
	base.BackupRoot, base.ManifestSHA256 = "/synthetic/external/backup", strings.Repeat("a", 64)
	mutations := map[string]func(*RestoreSourceRequest){
		"format":       func(r *RestoreSourceRequest) { r.FormatVersion = 2 },
		"relative":     func(r *RestoreSourceRequest) { r.BackupRoot = "relative" },
		"dirty":        func(r *RestoreSourceRequest) { r.BackupRoot = "/a/../b" },
		"control":      func(r *RestoreSourceRequest) { r.BackupRoot = "/a\nsecret_marker" },
		"digest":       func(r *RestoreSourceRequest) { r.ManifestSHA256 = "secret_marker" },
		"bytes-low":    func(r *RestoreSourceRequest) { r.MaximumBytes = (1 << 20) - 1 },
		"bytes-high":   func(r *RestoreSourceRequest) { r.MaximumBytes = (1 << 40) + 1 },
		"entries-low":  func(r *RestoreSourceRequest) { r.MaximumEntries = 3 },
		"entries-high": func(r *RestoreSourceRequest) { r.MaximumEntries = 1000001 },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			r := base
			change(&r)
			raw, _ := json.Marshal(r)
			_, e := LoadRestoreSourceRequest(raw, nil)
			if e == nil || strings.Contains(e.Error(), "secret_marker") {
				t.Fatal("invalid input/redaction", e)
			}
		})
	}
	raw, _ := json.Marshal(base)
	for _, env := range []string{"UNKNOWN=secret_marker", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES=false", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES=99999999999999999999", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT=$secret_marker", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT=\"secret_marker\"", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES=5\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_ENTRIES=6", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_BACKUP_ROOT="} {
		if _, e := LoadRestoreSourceRequest(raw, []byte(env)); e == nil || strings.Contains(e.Error(), "secret_marker") {
			t.Fatal("invalid env/redaction", e)
		}
	}
	for _, raw := range []string{`{"format_version":1,"format_version":1}`, `{"unknown":"secret_marker"}`, `null`} {
		if _, e := LoadRestoreSourceRequest([]byte(raw), nil); e == nil || strings.Contains(e.Error(), "secret_marker") {
			t.Fatal("unknown/duplicate/null input", e)
		}
	}
}
