package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestMVPNativeRestoreCompletionCustodyKeepsExactOriginalAndNeverOverwrites(t *testing.T) {
	root := mvpNativeRestoreCustodyRoot(t)
	plan, scopeID, request, connection, archive, _, _, inspector := mvpNativeRestoreActorFixture(t)
	complete := *inspector.output.NativePostgreSQLRestoreCompletion
	id := strings.Repeat("a", 32)
	file, got, e := retainMVPNativeRestoreCompletion(context.Background(), root, id, plan, scopeID, request, connection, archive, complete)
	if e != nil || !bytes.Equal(got.Original, complete.Original) || file.SHA256 != complete.OriginalSHA256 || inspector.inspections != 0 || inspector.reads != 0 {
		t.Fatal("original completion custody failed", e)
	}
	before, e := root.Stat(file.Name)
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := os.OpenRoot(root.Name())
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	again, e := readMVPNativeRestoreCompletion(context.Background(), reopened, id, file, plan, scopeID, request, connection, archive)
	after, se := root.Stat(file.Name)
	if e != nil || se != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() || !bytes.Equal(again.Original, complete.Original) {
		t.Fatal("readonly reopen changed original", e, se)
	}
	if _, _, e = retainMVPNativeRestoreCompletion(context.Background(), root, id, plan, scopeID, request, connection, archive, complete); e == nil {
		t.Fatal("original complete overwritten")
	}
}

func TestMVPNativeRestoreCompletionCustodyRejectsChangedDescriptorAndSourceWithoutRepair(t *testing.T) {
	for _, name := range []string{"digest", "size", "path", "raw", "mode", "missing", "archive", "request", "context"} {
		t.Run(name, func(t *testing.T) {
			root := mvpNativeRestoreCustodyRoot(t)
			plan, scopeID, request, connection, archive, _, _, inspector := mvpNativeRestoreActorFixture(t)
			complete := *inspector.output.NativePostgreSQLRestoreCompletion
			id := strings.Repeat("a", 32)
			file, _, e := retainMVPNativeRestoreCompletion(context.Background(), root, id, plan, scopeID, request, connection, archive, complete)
			if e != nil {
				t.Fatal(e)
			}
			switch name {
			case "digest":
				file.SHA256 = strings.Repeat("0", 64)
			case "size":
				file.Bytes++
			case "path":
				file.Name = "../" + file.Name
			case "raw":
				e = os.WriteFile(filepath.Join(root.Name(), file.Name), []byte("changed original\n"), 0600)
			case "mode":
				e = os.Chmod(filepath.Join(root.Name(), file.Name), 0644)
			case "missing":
				e = os.Rename(filepath.Join(root.Name(), file.Name), filepath.Join(root.Name(), file.Name+".retained"))
			case "archive":
				archive = strings.Repeat("0", 64)
			case "request":
				request.TargetDatabase = "different_target"
			case "context":
				connection.operator = "different_operator"
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = readMVPNativeRestoreCompletion(context.Background(), root, id, file, plan, scopeID, request, connection, archive); e == nil {
				t.Fatal("changed original/source accepted")
			}
			if name == "missing" {
				if _, e = root.Stat(file.Name); !os.IsNotExist(e) {
					t.Fatal("missing completion was repaired")
				}
			}
		})
	}
}

func mvpNativeRestoreCompletedObservationFixture(t *testing.T) (RestorePlan, string, wire.PostgreSQLRestoreRequest, restoreConnection, string, engine.NativePostgreSQLRestoreCompletion, engine.NativePostgreSQLRestoreObservation) {
	t.Helper()
	plan, scopeID, request, connection, archive, _, _, inspector := mvpNativeRestoreActorFixture(t)
	c := *inspector.output.NativePostgreSQLRestoreCompletion
	_, o := mvpNativeRestoreFixture()
	o.RestoreID = request.RestoreID
	o.ManifestSHA256 = c.ManifestSHA256
	o.IntentSHA256 = c.IntentSHA256
	o.FilesSHA256 = c.FilesSHA256
	o.TargetBindingSHA256 = c.TargetBindingSHA256
	o.PostDataSHA256 = c.PostDataSHA256
	r := o.NativePostgreSQL
	r.RequestSHA256 = wire.Digest(encode(request))
	r.ManifestSHA256 = c.ManifestSHA256
	r.IntentSHA256 = c.IntentSHA256
	r.FilesSHA256 = c.FilesSHA256
	r.CompletionSHA256 = c.OriginalSHA256
	r.TargetBindingSHA256 = c.TargetBindingSHA256
	r.PostDataSHA256 = c.PostDataSHA256
	o.Original = encode(o)
	o.OriginalSHA256 = wire.Digest(o.Original)
	return plan, scopeID, request, connection, archive, c, o
}

func TestMVPNativeRestoreCompletedObservationBindsActualOriginalCompletion(t *testing.T) {
	plan, scopeID, request, connection, archive, c, o := mvpNativeRestoreCompletedObservationFixture(t)
	got, e := bindMVPNativeRestoreCompletedObservation(plan, scopeID, request, connection, archive, c, o)
	if e != nil || got.NativePostgreSQL == nil || got.NativePostgreSQL.CompletionSHA256 != c.OriginalSHA256 {
		t.Fatal("matching originals refused", e)
	}
	for _, name := range []string{"completion", "intent", "files", "target", "postimage", "missing", "not-readonly"} {
		t.Run(name, func(t *testing.T) {
			plan, scopeID, request, connection, archive, c, o := mvpNativeRestoreCompletedObservationFixture(t)
			r := o.NativePostgreSQL
			switch name {
			case "completion":
				r.CompletionSHA256 = strings.Repeat("0", 64)
			case "intent":
				r.IntentSHA256 = strings.Repeat("0", 64)
				o.IntentSHA256 = r.IntentSHA256
			case "files":
				r.FilesSHA256 = strings.Repeat("0", 64)
				o.FilesSHA256 = r.FilesSHA256
			case "target":
				r.TargetBindingSHA256 = strings.Repeat("0", 64)
				o.TargetBindingSHA256 = r.TargetBindingSHA256
			case "postimage":
				r.PostDataSHA256 = strings.Repeat("0", 64)
				o.PostDataSHA256 = r.PostDataSHA256
			case "missing":
				o.OriginalCompletionPresent = false
				o.NativePostgreSQL = nil
			case "not-readonly":
				o.ReadOnly = false
			}
			o.Original = encode(o)
			o.OriginalSHA256 = wire.Digest(o.Original)
			if _, e := bindMVPNativeRestoreCompletedObservation(plan, scopeID, request, connection, archive, c, o); e == nil {
				t.Fatal("changed independent original attached to completion")
			}
		})
	}
}
