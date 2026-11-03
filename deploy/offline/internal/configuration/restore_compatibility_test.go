package configuration

import (
	"encoding/json"
	"testing"
)

func TestRestoreCompatibilityExplicitProtectedReference(t *testing.T) {
	r := RestoreRequest{RestoreSourceRequest: RestoreSourceDefaults(), Reason: "Reviewed repair restore", CompatibilityFile: "/synthetic/private/compatibility.json"}
	r.BackupRoot = "/synthetic/backup"
	r.ManifestSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	raw, _ := json.Marshal(r)
	got, e := LoadRestoreRequest(raw, []byte("PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE=/synthetic/alternate/compatibility.json\n"))
	if e != nil || got.CompatibilityFile != "/synthetic/alternate/compatibility.json" {
		t.Fatal(got, e)
	}
	for _, env := range []string{"PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE=\n", "PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE=relative\n", "PROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE=/a\nPROJECT_ONE_OFFLINE_RESTORE_COMPATIBILITY_FILE=/b\n"} {
		if _, e := LoadRestoreRequest(raw, []byte(env)); e == nil {
			t.Fatal("invalid/duplicate override accepted")
		}
	}
	for _, name := range []string{"relative", "/synthetic/../changed", "/synthetic/\nchanged"} {
		r.CompatibilityFile = name
		raw, _ := json.Marshal(r)
		if _, e := LoadRestoreRequest(raw, nil); e == nil {
			t.Fatal("invalid reference accepted")
		}
	}
}
