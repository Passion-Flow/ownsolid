package scope

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func completedRestoreSQLSnapshot(t *testing.T) (*locked, RestorePlan) {
	t.Helper()
	l, plan, base := restoreSQLSnapshotFixture(t)
	for index := range restoreSQLSteps() {
		events := appendSyntheticRestoreSQLStep(t, l.events, base, index, l.marker.ScopeID)
		for _, event := range events[len(events)-2:] {
			if e := l.appendEvent(event); e != nil {
				l.close()
				t.Fatal(e)
			}
		}
	}
	return l, plan
}

func TestRestoreQualificationInputBindsCompletedSQLAndImmutableTarget(t *testing.T) {
	l, plan := completedRestoreSQLSnapshot(t)
	defer l.close()
	before := encode(l.events)
	files, e := l.makeRestoreQualificationInput(context.Background(), plan)
	if e != nil {
		t.Fatal(e)
	}
	again, e := l.makeRestoreQualificationInput(context.Background(), plan)
	if e != nil || !reflect.DeepEqual(files, again) || !bytes.Equal(before, encode(l.events)) {
		t.Fatal("read-only deterministic constructor", e)
	}
	raw, e := restoreNamedFile(files, restoredRuntimeContextName)
	var r wire.RestoredRuntimeContext
	if e != nil || wire.StrictJSON(raw, &r) != nil || !r.Valid() || r.RestoreID != plan.AttemptID || r.SourceScopeID != plan.Source.SourceScopeID || r.TargetScopeID != l.marker.ScopeID || r.BackupManifestSHA256 != plan.Request.ManifestSHA256 || r.SQLArchiveSHA256 != restoreSQLState(l.events).record.Task.Restore.ArchiveSHA256 || r.TenantID != plan.Source.Source.TenantID || r.SchemaVersion != plan.Source.Source.SchemaVersion || r.TargetDatabase != "synthetic_restore" || r.TargetOperator != "synthetic_operator" || r.Reason != plan.Request.Reason {
		t.Fatal("SQL-bound generated context")
	}
	names := []string{}
	for _, file := range files {
		names = append(names, file.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("unstable payload")
	}
	for _, name := range []string{"auth-hmac", "mail-keyring.json", "future-key", "operator-dsn", "runtime-dsn"} {
		if _, e := restoreNamedFile(files, name); e != nil {
			t.Fatal("retained/new target file absent", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := l.makeRestoreQualificationInput(ctx, plan); e == nil {
		t.Fatal("cancellation ignored")
	}
	changed := plan
	changed.Request.Reason = "changed operation"
	if _, e := l.makeRestoreQualificationInput(context.Background(), changed); e == nil {
		t.Fatal("changed plan accepted")
	}
	last := l.events[len(l.events)-1]
	l.events = l.events[:len(l.events)-1]
	if _, e := l.makeRestoreQualificationInput(context.Background(), plan); e == nil {
		t.Fatal("incomplete SQL accepted")
	}
	l.events = append(l.events, last)
}

func TestRestoreQualificationInputRefusesCollisionAndMissingPrivateReferences(t *testing.T) {
	_, base, _ := restoreSQLJournalFixture(t)
	r := wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: base.Task.Owner.OperationID, SourceScopeID: strings.Repeat("a", 32), TargetScopeID: base.Task.Owner.ScopeID, BackupManifestSHA256: strings.Repeat("b", 64), SQLArchiveSHA256: strings.Repeat("c", 64), TenantID: base.Task.Restore.ExpectedSource.TenantID, SchemaVersion: 16, TargetDatabase: "synthetic_restore", TargetOperator: "synthetic_operator", Reason: "Synthetic private configuration merge"}
	for _, fault := range []string{"conflict", "duplicate", "reserved", "empty", "traversal", "missing-auth", "missing-mail", "outside-key", "outside-sso", "wrong-database", "wrong-operator", "same-scope", "missing-runtime"} {
		t.Run(fault, func(t *testing.T) {
			p, model, _, _ := restoreTargetInputFixture()
			context := r
			switch fault {
			case "conflict":
				p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "operator-dsn", Contents: []byte("different")})
			case "duplicate":
				p.files["control-config"] = append(p.files["control-config"], p.files["control-config"][2])
			case "reserved":
				p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: restoredRuntimeContextName, Contents: []byte("old")})
			case "empty":
				p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "empty", Contents: []byte{}})
			case "traversal":
				p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "../outside", Contents: []byte("bad")})
			case "missing-auth", "missing-mail":
				name := "auth-hmac"
				if fault == "missing-mail" {
					name = "mail-keyring.json"
				}
				for i, f := range p.files["control-config"] {
					if f.Name == name {
						p.files["control-config"] = append(p.files["control-config"][:i], p.files["control-config"][i+1:]...)
						break
					}
				}
			case "outside-key":
				for i, f := range p.files["operator-config"] {
					if f.Name == "control.yaml" {
						p.files["operator-config"][i].Contents = bytes.ReplaceAll(f.Contents, []byte(engine.DatabaseTaskDestination+"/private/auth-hmac"), []byte("/old/source/private/auth-hmac"))
					}
				}
			case "outside-sso":
				for i, f := range p.files["operator-config"] {
					if f.Name == "control.yaml" {
						var v map[string]any
						if wire.UniqueJSON(f.Contents, &v) != nil {
							t.Fatal("fixture")
						}
						v["sso"] = map[string]string{"keyring_file": "/old/source/private/sso-key"}
						p.files["operator-config"][i].Contents = encode(v)
					}
				}
			case "wrong-database":
				context.TargetDatabase = "outside"
			case "wrong-operator":
				context.TargetOperator = "outside"
			case "same-scope":
				context.SourceScopeID = context.TargetScopeID
			case "missing-runtime":
				replaceRestoreTestFile(&p, "operator-config", "runtime-dsn", nil)
			}
			if _, e := mergeRestoreQualificationInput(p, model, context); e == nil {
				t.Fatal("unsafe generated input accepted")
			}
		})
	}
	p, model, _, _ := restoreTargetInputFixture()
	files, e := mergeRestoreQualificationInput(p, model, r)
	if e != nil {
		t.Fatal("positive merged input", e)
	}
	key, _ := restoreNamedFile(files, "auth-hmac")
	key[0] ^= 1
	original, _ := restoreNamedFile(p.files["control-config"], "auth-hmac")
	if bytes.Equal(key, original) {
		t.Fatal("constructor aliases immutable source")
	}
}
