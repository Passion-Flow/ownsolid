package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestCumulativeUpgradePlanPublicationAndObservedRecovery(t *testing.T) {
	for _, state := range []string{"new", "already-visible", "changed", "missing-observation", "symlink"} {
		t.Run(state, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "private")
			if e := os.Mkdir(directory, 0700); e != nil {
				t.Fatal(e)
			}
			root, e := os.OpenRoot(directory)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			l := &locked{root: root, config: configuration.Defaults()}
			plan := CumulativeUpgradePlan{FormatVersion: 1, ScopeID: strings.Repeat("a", 32), GenerationID: strings.Repeat("b", 32)}
			expected := encode(plan)
			name := filepath.Join(directory, cumulativeUpgradePlanFile)
			var before os.FileInfo
			if state == "already-visible" || state == "changed" || state == "symlink" {
				raw := expected
				if state == "changed" {
					raw = []byte("preserved-foreign-plan\n")
				}
				put(t, name, raw)
				if state == "symlink" {
					if e := os.Rename(name, name+"-retained"); e != nil {
						t.Fatal(e)
					}
					if e := os.Symlink(cumulativeUpgradePlanFile+"-retained", name); e != nil {
						t.Fatal(e)
					}
				}
				before, e = os.Lstat(name)
				if e != nil {
					t.Fatal(e)
				}
			}
			record := CumulativeUpgradeRecord{Step: 0, Phase: "plan-publish", GenerationID: plan.GenerationID}
			_, e = l.executeCumulativeStep(context.Background(), nil, nil, plan, compose.Model{}, nil, record, state != "new")
			if state == "changed" || state == "missing-observation" || state == "symlink" {
				if e == nil {
					t.Fatal("incomplete or changed publication accepted")
				}
				if state == "missing-observation" {
					if _, e := os.Lstat(name); !os.IsNotExist(e) {
						t.Fatal("observation created a missing plan", e)
					}
					return
				}
				after, statError := os.Lstat(name)
				if statError != nil || !os.SameFile(before, after) {
					t.Fatal("failed publication replaced retained input", statError)
				}
				if state == "changed" {
					raw, _ := os.ReadFile(name)
					if string(raw) != "preserved-foreign-plan\n" {
						t.Fatal("changed plan overwritten")
					}
				}
				return
			}
			if e != nil {
				t.Fatal("fixed plan publication failed", e)
			}
			after, e := os.Lstat(name)
			raw, readError := os.ReadFile(name)
			if e != nil || readError != nil || !bytes.Equal(raw, expected) || after.Mode() != 0600 || before != nil && !os.SameFile(before, after) {
				t.Fatal("durable exact plan publication changed identity or bytes", e, readError)
			}
			// A later observation reopens and syncs the same already-visible plan;
			// it must not submit any Engine effect or overwrite the file.
			_, e = l.executeCumulativeStep(context.Background(), nil, nil, plan, compose.Model{}, nil, record, true)
			reopened, statError := os.Lstat(name)
			if e != nil || statError != nil || !os.SameFile(after, reopened) {
				t.Fatal("publication observation replaced the plan", e, statError)
			}
			if e := l.syncNamedRestorePlan(cumulativeUpgradePlanFile+".other", expected); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_PLAN_NAME_INVALID" {
				t.Fatal("unrecognized plan name admitted", e)
			}
		})
	}
}
