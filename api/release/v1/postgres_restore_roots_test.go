package release

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func measuredRestoreRoots() []PostgreSQLRestoreRoot {
	return []PostgreSQLRestoreRoot{{"/target/models", 1, 10, 10001, 10001}, {"/target/datasets", 2, 20, 10001, 10001}, {"/target/training", 3, 30, 10001, 10001}, {"/target/preparations", 4, 40, 10001, 10001}}
}

func TestPostgresRestoreMeasuredRootsKeepLegacyOmission(t *testing.T) {
	legacy := PostgreSQLRestoreRequest{Reason: "Original request encoding"}
	raw, e := json.Marshal(legacy)
	if e != nil || bytes.Contains(raw, []byte("target_roots")) {
		t.Fatal("absent measurement changed legacy encoding", e)
	}
	var roundtrip PostgreSQLRestoreRequest
	if e = StrictJSON(raw, &roundtrip); e != nil || roundtrip.TargetRoots != nil {
		t.Fatal("legacy request refused", e)
	}
	again, e := json.Marshal(roundtrip)
	if e != nil || !bytes.Equal(raw, again) {
		t.Fatal("legacy canonical bytes changed", e)
	}
	legacy.TargetRoots = measuredRestoreRoots()
	raw, e = json.Marshal(legacy)
	if e != nil || StrictJSON(raw, &roundtrip) != nil || !ValidPostgreSQLRestoreRoots(roundtrip.TargetRoots) {
		t.Fatal("valid measured roots refused", e)
	}
}

func TestPostgresRestoreMeasuredRootsRefuseInvalidOrAmbiguousFacts(t *testing.T) {
	for name, change := range map[string]func([]PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot{
		"empty":         func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { return v[:0] },
		"missing-store": func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { return v[:3] },
		"extra-store":   func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { return append(v, v[0]) },
		"same-path":     func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[1].Path = v[0].Path; return v },
		"nested-path":   func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[1].Path = v[0].Path + "/nested"; return v },
		"parent-path":   func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[1].Path = "/target"; return v },
		"same-inode": func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot {
			v[1].Device = v[0].Device
			v[1].Inode = v[0].Inode
			return v
		},
		"relative":     func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].Path = "models"; return v },
		"unclean":      func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].Path = "/target/../models"; return v },
		"device":       func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].Device = 0; return v },
		"inode":        func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].Inode = 0; return v },
		"root-user":    func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].UID = 0; return v },
		"root-group":   func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].GID = 0; return v },
		"uid-overflow": func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].UID = 2147483648; return v },
		"gid-overflow": func(v []PostgreSQLRestoreRoot) []PostgreSQLRestoreRoot { v[0].GID = 2147483648; return v },
	} {
		t.Run(name, func(t *testing.T) {
			roots := change(measuredRestoreRoots())
			if ValidPostgreSQLRestoreRoots(roots) {
				t.Fatal("invalid facts admitted")
			}
			raw, e := json.Marshal(PostgreSQLRestoreRequest{TargetRoots: roots})
			if e != nil {
				t.Fatal(e)
			}
			// Empty slices are omitted by encoding/json; test the explicit wire
			// spelling below. Nonempty malformed measurements must refuse here.
			var r PostgreSQLRestoreRequest
			if len(roots) > 0 && StrictJSON(raw, &r) == nil {
				t.Fatal("invalid wire facts admitted")
			}
		})
	}
	root, _ := json.Marshal(measuredRestoreRoots())
	valid := `{"target_roots":` + string(root) + `}`
	for _, raw := range []string{
		`{"target_roots":null}`, `{"target_roots":false}`, `{"target_roots":[]}`,
		strings.Replace(valid, "target_roots", "TARGET_ROOTS", 1),
		strings.Replace(valid, `"path"`, `"PATH"`, 1),
		strings.Replace(valid, `"device":1`, `"device":1,"device":1`, 1),
		strings.Replace(valid, `"device":1`, `"device":1,"DEVICE":1`, 1),
		strings.Replace(valid, `"device":1,`, ``, 1),
		strings.Replace(valid, `"device":1`, `"device":true`, 1),
		strings.Replace(valid, `"device":1`, `"device":1.5`, 1),
		strings.Replace(valid, `"device":1`, `"device":-1`, 1),
		strings.Replace(valid, `"device":1`, `"device":18446744073709551616`, 1),
		strings.Replace(valid, `"inode":10`, `"inode":null`, 1),
		strings.Replace(valid, `"inode":10`, `"inode":10,"unknown":1`, 1),
		strings.TrimSuffix(valid, "}") + `,"target_roots":` + string(root) + `}`,
	} {
		var r PostgreSQLRestoreRequest
		if StrictJSON([]byte(raw), &r) == nil {
			t.Fatal("ambiguous wire facts admitted", raw)
		}
	}
}
