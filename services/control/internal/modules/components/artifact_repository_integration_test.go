//go:build repository_integration && component_artifact_integration

package components_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func pairedComponentArtifacts(t *testing.T) pairedComponentStory {
	t.Helper()
	g := pairedComponentSetup(t)
	directory := os.Getenv("PROJECT_ONE_ACTUAL_COMPONENT_CATALOG")
	if !filepath.IsAbs(directory) {
		t.Fatal("COMPONENT_REAL_APPROVED_CATALOG_REQUIRED")
	}
	g.s.CatalogDirectory = directory
	for _, version := range []string{"1.12.0", wire.NodeExporterVersion} {
		file, _, e := g.s.OpenArtifactVersionContext(g.ctx, version)
		if e != nil {
			t.Fatal("actual approved archive required", e)
		}
		file.Close()
	}
	return g
}
func pairedComponentDownload(t *testing.T, d httpserver.Download, version string) {
	t.Helper()
	if d.File == nil {
		t.Fatal("missing descriptor")
	}
	defer d.File.Close()
	recipe, _ := wire.RecipeForVersion(version)
	hash := sha256.New()
	count, e := io.Copy(hash, d.File)
	if e != nil || count != recipe.ArchiveBytes || d.Size != count || d.SHA256 != recipe.ArchiveSHA256 || hex.EncodeToString(hash.Sum(nil)) != recipe.ArchiveSHA256 {
		t.Fatal("actual archive bytes mismatch", e)
	}
}
func TestActualPairedComponentArtifacts(t *testing.T) {
	t.Run("real_approved_package_install_upgrade_rollback_and_exact_replay", func(t *testing.T) {
		g := pairedComponentArtifacts(t)
		c := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		key := uuid.NewString()
		input := components.Request{Action: "install", Version: "1.12.0", Config: &c, Reason: "Real approved archive synthetic machine install"}
		task, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, input, key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		claim := g.claim(t, task)
		d, e := g.s.DownloadArtifact(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken)
		if e != nil {
			t.Fatal(e)
		}
		pairedComponentDownload(t, d, "1.12.0")
		o := maintenanceObservation("1.12.0", &c)
		if _, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		original := g.s.CatalogDirectory
		g.s.CatalogDirectory = filepath.Join(t.TempDir(), "absent")
		again, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, input, key, uuid.NewString())
		g.s.CatalogDirectory = original
		if e != nil || again.ID != task.ID {
			t.Fatal("exact replay required archive again", e)
		}
		upgrade, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, components.Request{Action: "upgrade", Version: wire.NodeExporterVersion, Reason: "Real approved forward archive"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		claim = g.claim(t, upgrade)
		d, e = g.s.DownloadArtifact(g.ctx, g.node.NodeID, g.node.Credential, upgrade.ID, claim.Lease.LeaseToken)
		if e != nil {
			t.Fatal(e)
		}
		pairedComponentDownload(t, d, wire.NodeExporterVersion)
		o = maintenanceObservation(wire.NodeExporterVersion, &c)
		o.PreviousVersion = "1.12.0"
		if _, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, upgrade.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		rollback := g.request(t, "rollback")
		g.complete(t, rollback, maintenanceObservation("1.12.0", &c), nil)
		out, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e != nil || out.Version != "1.12.0" || out.DesiredVersion != "1.12.0" {
			t.Fatal("actual SQL rollback persistence", e)
		}
	})
	t.Run("exact_current_lease_credential_and_actual_HTTP_package_route", func(t *testing.T) {
		g := pairedComponentArtifacts(t)
		task := g.request(t, "install")
		claim := g.claim(t, task)
		d, e := g.s.DownloadArtifact(g.ctx, g.node.NodeID, strings.Repeat("A", 43), task.ID, claim.Lease.LeaseToken)
		if e == nil || d.File != nil {
			t.Fatal("wrong credential got package", e)
		}
		d, e = g.s.DownloadArtifact(g.ctx, g.node.NodeID, g.node.Credential, task.ID, strings.Repeat("A", 43))
		if e == nil || d.File != nil {
			t.Fatal("wrong lease got package", e)
		}
		h := &nodehttp.Handler{Service: g.s.Nodes, ComponentArtifact: g.s.DownloadArtifact}
		r := httptest.NewRequest("POST", "/agent/v1/nodes/"+g.node.NodeID+"/tasks/"+task.ID+"/artifact", strings.NewReader(`{"lease_token":"`+claim.Lease.LeaseToken+`"}`))
		r.Header.Set("Authorization", "Bearer "+g.node.Credential)
		out, status, e := h.Machine(r, uuid.NewString())
		if e != nil || status != 200 {
			t.Fatal("actual owning package HTTP", e)
		}
		pairedComponentDownload(t, out.(httpserver.Download), wire.NodeExporterVersion)
		current, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Nodes.Update(g.ctx, g.admin, current.ID, current.Name, "disabled", current.Version, "Fence package current authority", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		d, e = g.s.DownloadArtifact(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken)
		if e == nil || d.File != nil {
			t.Fatal("fenced task got package", e)
		}
	})
	t.Run("unknown_final_fence_zero_descriptor_and_immutable_source_refusals", func(t *testing.T) {
		g := pairedComponentArtifacts(t)
		task := g.request(t, "install")
		claim := g.claim(t, task)
		hook := &pairedComponentOutcomeStore{Store: g.f.Store, at: 2}
		s := g.wrapped(hook)
		d, e := s.DownloadArtifact(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken)
		pairedComponentCode(t, e, string(transaction.CommitUnknown))
		if d.File != nil || d.Size != 0 || d.SHA256 != "" {
			t.Fatal("uncertain descriptor published")
		}
		// Change only private disposable copies; retain original approved input intact.
		directory := t.TempDir()
		source := filepath.Join(g.s.CatalogDirectory, wire.NodeExporterVersion, "linux-amd64", "node_exporter-"+wire.NodeExporterVersion+".linux-amd64.tar.gz")
		raw, e := os.ReadFile(source)
		if e != nil {
			t.Fatal(e)
		}
		raw[0] ^= 1
		corrupt := filepath.Join(directory, "corrupt.tar.gz")
		if e = os.WriteFile(corrupt, raw, 0600); e != nil {
			t.Fatal(e)
		}
		invalid := &components.Service{ArchiveFile: corrupt}
		file, _, e := invalid.OpenArtifactVersion(wire.NodeExporterVersion)
		pairedComponentCode(t, e, "COMPONENT_ASSET_INVALID")
		if file != nil {
			file.Close()
			t.Fatal("corrupt descriptor exposed")
		}
		alias := filepath.Join(directory, "archive-link")
		if e = os.Symlink(source, alias); e != nil {
			t.Fatal(e)
		}
		invalid.ArchiveFile = alias
		if file, _, e = invalid.OpenArtifactVersion(wire.NodeExporterVersion); e == nil || file != nil {
			t.Fatal("symlink source accepted", e)
		}
		invalid.ArchiveFile = source
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if file, _, e = invalid.OpenArtifactVersionContext(ctx, wire.NodeExporterVersion); e == nil || file != nil {
			t.Fatal("cancelled hash exposed descriptor", e)
		}
		current, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil || !current.ArtifactAvailable || current.ReceivedAt != nil && current.ReceivedAt.After(time.Now().UTC()) {
			t.Fatal("original archive damaged", e)
		}
	})
}
