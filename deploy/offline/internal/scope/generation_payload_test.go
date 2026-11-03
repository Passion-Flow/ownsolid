package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/tlsidentity"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Real private files and durability/readback, with explicitly synthetic trusted
// plan/journal metadata. No PKI admission, SQL, Engine or serving is fabricated.
func generationPayloadFixture(t *testing.T) (*locked, RestoredTLSPlan, []engine.ConfigurationFile, []byte) {
	t.Helper()
	root, _, base := privateSnapshotFixture(t, false)
	c := configuration.Defaults()
	c.ScopeRoot = base
	request := configuration.TLSGenerationRequest{FormatVersion: 2, Target: "machine", CertificateFile: filepath.Join(base, "source-chain.pem"), PrivateKeyFile: filepath.Join(base, "source-key.pem"), TrustBundleFile: filepath.Join(base, "source-trust.pem"), Reason: "Synthetic private custody unit fixture"}
	files := []engine.ConfigurationFile{{Name: "certificate.pem", Contents: []byte("synthetic-chain-bytes")}, {Name: "key.pem", Contents: []byte("synthetic-private-key-bytes")}, {Name: "runtime.yaml", Contents: []byte("synthetic-runtime-bytes")}}
	trust := []byte("synthetic-explicit-trust-bytes")
	for name, raw := range map[string][]byte{request.CertificateFile: files[0].Contents, request.PrivateKeyFile: files[1].Contents, request.TrustBundleFile: trust} {
		put(t, name, raw)
	}
	p := RestoredTLSPlan{FormatVersion: 2, ScopeID: strings.Repeat("a", 32), GenerationID: strings.Repeat("b", 32), Request: request, CertificateName: "certificate.pem", PrivateKeyName: "key.pem", Identity: tlsidentity.Identity{TrustBundleSHA256: wire.Digest(trust)}}
	for _, f := range files {
		p.ConfigurationFiles = append(p.ConfigurationFiles, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	p.PayloadSHA256 = restoreConfigurationDigest(files)
	l := &locked{root: root, config: c, marker: Marker{ScopeID: p.ScopeID}}
	r := tlsProjectionPlanRecord(p)
	l.events = []Event{{Action: "tls_projection_effect", Outcome: "started", OperationID: strings.Repeat("c", 32), TLSProjection: &r}}
	return l, p, files, trust
}

func TestGenerationPayloadPrivateDurabilityAndSourceIndependence(t *testing.T) {
	l, p, files, trust := generationPayloadFixture(t)
	if e := l.writeGenerationPayload(context.Background(), p, files, trust); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{p.Request.CertificateFile, p.Request.PrivateKeyFile, p.Request.TrustBundleFile} {
		if e := os.Remove(name); e != nil {
			t.Fatal(e)
		}
	}
	if e := l.root.Close(); e != nil {
		t.Fatal(e)
	}
	root, e := openPrivateDirectory(l.config.ScopeRoot)
	if e != nil {
		t.Fatal(e)
	}
	l.root = root
	defer root.Close()
	actual, actualTrust, e := l.readGenerationPayload(context.Background(), p)
	if e != nil || !reflect.DeepEqual(files, actual) || !bytes.Equal(trust, actualTrust) {
		t.Fatal("private restart/deleted source readback", e)
	}
	if e = l.syncGenerationPayload(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	actual[0].Contents[0] ^= 1
	again, _, e := l.readGenerationPayload(context.Background(), p)
	if e != nil || !reflect.DeepEqual(files, again) {
		t.Fatal("returned bytes aliased durable data", e)
	}
	descriptor, e := regular(root, generationPayloadDirectory(p.GenerationID)+"/descriptor.json", l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal(e)
	}
	encoded, e := json.Marshal(actual)
	if e != nil {
		t.Fatal(e)
	}
	for _, raw := range append([][]byte{trust}, files[0].Contents, files[1].Contents, files[2].Contents) {
		if bytes.Contains(descriptor, raw) || bytes.Contains(encoded, raw) {
			t.Fatal("private contents serialized")
		}
	}
}

func TestGenerationPayloadRejectsInventoryAndSnapshotDrift(t *testing.T) {
	for _, fault := range []string{"extra-root", "extra-file", "missing-file", "key-change", "trust-change", "descriptor-change", "file-mode", "directory-mode", "hardlink", "symlink", "wrong-plan", "wrong-generation", "unrecorded", "wrong-custody-flag", "wrong-request"} {
		t.Run(fault, func(t *testing.T) {
			l, p, files, trust := generationPayloadFixture(t)
			if e := l.writeGenerationPayload(context.Background(), p, files, trust); e != nil {
				t.Fatal(e)
			}
			directory := filepath.Join(l.config.ScopeRoot, generationPayloadDirectory(p.GenerationID))
			key := filepath.Join(directory, "configuration/key.pem")
			switch fault {
			case "extra-root":
				put(t, filepath.Join(directory, "foreign"), []byte("retain"))
			case "extra-file":
				put(t, filepath.Join(directory, "configuration/foreign"), []byte("retain"))
			case "missing-file":
				if e := os.Remove(key); e != nil {
					t.Fatal(e)
				}
			case "key-change":
				put(t, key, []byte("changed-private-key"))
			case "trust-change":
				put(t, filepath.Join(directory, "trust.pem"), []byte("changed-trust"))
			case "descriptor-change":
				put(t, filepath.Join(directory, "descriptor.json"), []byte("{}\n"))
			case "file-mode":
				if e := os.Chmod(key, 0644); e != nil {
					t.Fatal(e)
				}
			case "directory-mode":
				if e := os.Chmod(filepath.Join(directory, "configuration"), 0755); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(key, filepath.Join(l.config.ScopeRoot, "alias")); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Rename(key, key+"-saved"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink("key.pem-saved", key); e != nil {
					t.Fatal(e)
				}
			case "wrong-plan":
				p.PayloadSHA256 = strings.Repeat("f", 64)
			case "wrong-generation":
				p.GenerationID = strings.Repeat("d", 32)
			case "unrecorded":
				l.events = nil
			case "wrong-custody-flag":
				l.events[0].TLSProjection.PayloadCustody = false
			case "wrong-request":
				p.Request.Target = "invalid"
			}
			actual, actualTrust, e := l.readGenerationPayload(context.Background(), p)
			if e == nil || actual != nil || actualTrust != nil {
				t.Fatal("changed custody returned private bytes")
			}
			if _, e := os.Lstat(directory); e != nil {
				t.Fatal("reader destroyed evidence", e)
			}
		})
	}
}

func TestGenerationPayloadPublicationDoesNotOverwrite(t *testing.T) {
	for _, fault := range []string{"complete", "partial", "unrecorded", "cancelled", "not-pending", "missing-reconcile"} {
		t.Run(fault, func(t *testing.T) {
			l, p, files, trust := generationPayloadFixture(t)
			directory := filepath.Join(l.config.ScopeRoot, generationPayloadDirectory(p.GenerationID))
			if fault == "missing-reconcile" {
				if e := l.syncGenerationPayload(context.Background(), p); e == nil {
					t.Fatal("missing custody reconciled")
				}
				if _, e := os.Lstat(directory); !os.IsNotExist(e) {
					t.Fatal("reconcile created custody")
				}
				return
			}
			if fault == "complete" {
				if e := l.writeGenerationPayload(context.Background(), p, files, trust); e != nil {
					t.Fatal(e)
				}
			}
			if fault == "partial" {
				if e := os.Mkdir(directory, 0700); e != nil {
					t.Fatal(e)
				}
				put(t, filepath.Join(directory, "foreign"), []byte("preserve-unknown"))
			}
			if fault == "unrecorded" {
				l.events = nil
			}
			if fault == "not-pending" {
				l.events[0].Outcome = "completed"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			if e := l.writeGenerationPayload(ctx, p, files, trust); e == nil {
				t.Fatal("invalid/replayed custody write succeeded")
			}
			if fault == "complete" {
				actual, _, e := l.readGenerationPayload(context.Background(), p)
				if e != nil || !reflect.DeepEqual(actual, files) {
					t.Fatal("occupied custody changed", e)
				}
			} else if fault == "partial" {
				raw, e := os.ReadFile(filepath.Join(directory, "foreign"))
				if e != nil || string(raw) != "preserve-unknown" {
					t.Fatal("partial overwritten", e)
				}
			} else if _, e := os.Lstat(directory); !os.IsNotExist(e) {
				t.Fatal("refused write created output")
			}
		})
	}
}

func TestGenerationPayloadAbortIsExactAndPreservesUnknown(t *testing.T) {
	for _, fault := range []string{"valid", "missing-file", "missing-directory", "unknown", "changed", "hardlink", "no-abort-intent", "consumed", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			l, p, files, trust := generationPayloadFixture(t)
			if e := l.writeGenerationPayload(context.Background(), p, files, trust); e != nil {
				t.Fatal(e)
			}
			directory := filepath.Join(l.config.ScopeRoot, generationPayloadDirectory(p.GenerationID))
			r := tlsProjectionPlanRecord(p)
			if fault != "no-abort-intent" {
				l.events = append(l.events, Event{Action: "tls_projection_abort", Outcome: "started", OperationID: strings.Repeat("d", 32), TLSProjection: &r})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch fault {
			case "missing-file":
				if e := os.Remove(filepath.Join(directory, "configuration/key.pem")); e != nil {
					t.Fatal(e)
				}
			case "missing-directory":
				if e := l.removeGenerationPayload(ctx, p); e != nil {
					t.Fatal(e)
				}
			case "unknown":
				put(t, filepath.Join(directory, "configuration/foreign"), []byte("preserve-unknown"))
			case "changed":
				put(t, filepath.Join(directory, "configuration/key.pem"), []byte("preserve-changed"))
			case "hardlink":
				if e := os.Link(filepath.Join(directory, "configuration/key.pem"), filepath.Join(l.config.ScopeRoot, "foreign-link")); e != nil {
					t.Fatal(e)
				}
			case "consumed":
				l.events = append(l.events, Event{ControlReplacement: &ControlReplacementRecord{GenerationID: p.GenerationID}})
			case "cancelled":
				cancel()
			}
			e := l.removeGenerationPayload(ctx, p)
			good := fault == "valid" || fault == "missing-file" || fault == "missing-directory"
			if good {
				if e != nil {
					t.Fatal("exact abort refused", e)
				}
				if _, e := os.Lstat(directory); !os.IsNotExist(e) {
					t.Fatal("raw custody remains after completed abort")
				}
			} else {
				if e == nil {
					t.Fatal("unsafe cleanup succeeded")
				}
				if _, e := os.Lstat(filepath.Join(directory, "descriptor.json")); e != nil {
					t.Fatal("cleanup deleted known files before rejecting unsafe inventory", e)
				}
			}
			unrelated, e := os.ReadFile(filepath.Join(l.config.ScopeRoot, "payload"))
			if e != nil || string(unrelated) != "synthetic-private-payload" {
				t.Fatal("unrelated input changed", e)
			}
		})
	}
}
