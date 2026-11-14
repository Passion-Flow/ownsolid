//go:build repository_integration

package applications_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Actual offline processor readiness and actual lifecycle/configuration SQL only.
// There is no admitted interpreter, retrieved answer, provider or gateway result.
func TestActualPairedPortalProcessorProjection(t *testing.T) {
	t.Run("actual_processor_current_readiness_parent_cancel_and_stop", func(t *testing.T) {
		g := portalSetup(t)
		repository := os.Getenv("PROJECT_ONE_REPOSITORY_ROOT")
		python, model := os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY")
		if repository == "" || !filepath.IsAbs(repository) || python == "" || model == "" {
			t.Fatal("explicit source-bound actual offline encoder inputs required")
		}
		scratch := t.TempDir()
		if e := os.Chmod(scratch, 0700); e != nil {
			t.Fatal(e)
		}
		g.owner.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: python, WorkerFile: filepath.Join(repository, "runtimes/knowledge/embed.py"), ModelDirectory: model, ScratchRoot: scratch}
		d := &distribution.Service{Identity: g.owner, Workspaces: g.apps.Workspaces}
		k := &knowledge.Service{Identity: g.owner, Workspaces: g.apps.Workspaces, Distribution: d}
		if e := k.OpenConfiguredProcessor(g.ctx); e != nil {
			t.Fatal("actual configured CPU encoder readiness", e)
		}
		t.Cleanup(k.Processor.Close)
		flow := g.apps.Workflows
		flow.Knowledge = k
		flow.Distribution = d
		v := g.draft(t, "Actual current lifecycle projection")
		version := g.publication(t, v, 1)
		g.conversation(t, v.ID, version, g.f.User)
		parent, cancel := context.WithCancel(g.ctx)
		t.Cleanup(cancel)
		if e := flow.Start(parent); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if e := flow.Stop(ctx); e != nil {
				t.Error("actual maintenance join", e)
			}
		})
		page, e := g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 1 || !page.Items[0].CanExecute {
			t.Fatal("real current ready availability", e)
		}
		history, e := g.apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(history.Items) != 1 || !history.Items[0].CanExecute {
			t.Fatal("real current private ready availability", e)
		}
		// No publisher/Stop is called between cancellation and these actual reads.
		cancel()
		page, e = g.apps.List(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 1 || page.Items[0].CanExecute {
			t.Fatal("cancelled lifecycle remained projected ready", e)
		}
		history, e = g.apps.Conversations(g.ctx, g.a, g.f.Workspace, url.Values{})
		if e != nil || len(history.Items) != 1 || history.Items[0].CanExecute {
			t.Fatal("cancelled private lifecycle remained ready", e)
		}
		if e = flow.Stop(g.ctx); e != nil {
			t.Fatal(e)
		}
		if e = flow.VerifyStored(g.ctx); e == nil {
			t.Fatal("stopped lifecycle reported ready")
		}
	})
}
