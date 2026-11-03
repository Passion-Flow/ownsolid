package healthprobe

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestHealthProbeProtectedTrustInputs(t *testing.T) {
	_, valid := syntheticCertificate(t, []string{"portal.example.invalid"}, nil, false)
	for _, name := range []string{
		"valid", "valid_bundle", "missing", "empty", "oversized", "file_mode", "directory_mode", "file_symlink", "parent_symlink", "hardlink", "fifo", "unknown_pem", "private_key", "leading_garbage", "trailing_garbage", "invalid_der", "malformed_then_valid", "pem_headers",
	} {
		t.Run(name, func(t *testing.T) {
			path := syntheticTrustPath(t, valid)
			write := func(raw []byte) {
				t.Helper()
				if e := os.WriteFile(path, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			switch name {
			case "valid_bundle":
				write(append(append([]byte(" \n"), valid...), valid...))
			case "missing":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			case "empty":
				write(nil)
			case "oversized":
				write(bytes.Repeat([]byte("x"), int(maximumTrustBytes)+1))
			case "file_mode":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "directory_mode":
				if e := os.Chmod(filepath.Dir(path), 0755); e != nil {
					t.Fatal(e)
				}
			case "file_symlink":
				link := filepath.Join(filepath.Dir(path), "link.pem")
				if e := os.Symlink(path, link); e != nil {
					t.Fatal(e)
				}
				path = link
			case "parent_symlink":
				link := filepath.Join(filepath.Dir(filepath.Dir(path)), "linked-private")
				if e := os.Symlink(filepath.Dir(path), link); e != nil {
					t.Fatal(e)
				}
				path = filepath.Join(link, filepath.Base(path))
			case "hardlink":
				if e := os.Link(path, filepath.Join(filepath.Dir(path), "second-link.pem")); e != nil {
					t.Fatal(e)
				}
			case "fifo":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
				if e := syscall.Mkfifo(path, 0600); e != nil {
					t.Fatal(e)
				}
			case "unknown_pem":
				write(pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN", Bytes: []byte("synthetic")}))
			case "private_key":
				write(append(append([]byte(nil), valid...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("synthetic unparsed key")})...))
			case "leading_garbage":
				write(append([]byte("ignored text must not be accepted\n"), valid...))
			case "trailing_garbage":
				write(append(append([]byte(nil), valid...), []byte("trailing text")...))
			case "invalid_der":
				write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("synthetic invalid DER")}))
			case "malformed_then_valid":
				write(append([]byte("-----BEGIN CERTIFICATE-----\nnot-base64\n-----END CERTIFICATE-----\n"), valid...))
			case "pem_headers":
				block, _ := pem.Decode(valid)
				block.Headers = map[string]string{"Unexpected": "synthetic"}
				write(pem.EncodeToMemory(block))
			}
			pool, e := readTrustPool(path)
			if name == "valid" || name == "valid_bundle" {
				if e != nil || pool == nil {
					t.Fatal("valid explicitly private certificate bundle refused", e)
				}
				return
			}
			healthFailure(t, e, trustInvalid)
		})
	}
	// A trust read failure occurs before the only possible dial, even when a
	// real loopback target is available. No database or product handler exists.
	observation := &syntheticHealthObservation{}
	server := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), observation)
	configuration := syntheticServerConfiguration(server, config.TransportDirectHTTPS)
	input := syntheticInput(server, syntheticTrustPath(t, []byte("not a certificate")))
	healthFailure(t, Run(context.Background(), configuration, input), trustInvalid)
	if requests, _ := observation.snapshot(); len(requests) != 0 || observation.accepts.Load() != 0 {
		t.Fatal("invalid trust reached network")
	}
}

func TestHealthProbeTrustParentOpenRefusesPostcheckFIFO(t *testing.T) {
	const childInput = "PROJECT_ONE_TEST_PROBE_TRUST_PARENT_CHILD"
	const fixtureMarker = "projectone-owned-trust-parent-fixture-v1\n"
	if parent := os.Getenv(childInput); parent != "" {
		if filepath.Base(parent) != "synthetic-parent" {
			t.Fatal("exclusive disposable trust parent fixture required")
		}
		marker, e := openTrustSnapshot(filepath.Join(filepath.Dir(parent), "trust-parent-fixture"))
		if e != nil {
			t.Fatal("exclusive disposable trust parent fixture required")
		}
		raw, readError := marker.read()
		marker.close()
		if readError != nil || !bytes.Equal(raw, []byte(fixtureMarker)) {
			t.Fatal("owned trust parent fixture marker invalid")
		}
		before, e := os.Lstat(parent)
		if e != nil || !trustOwned(before, true) {
			t.Fatal("owned directory precheck failed")
		}
		if e = os.Rename(parent, parent+".moved"); e != nil {
			t.Fatal("owned directory replacement failed")
		}
		if e = syscall.Mkfifo(parent, 0600); e != nil {
			t.Fatal("owned parent FIFO fixture failed")
		}
		root, e := openTrustDirectory(parent)
		if root != nil {
			_ = root.Close()
		}
		if !errors.Is(e, syscall.ENOTDIR) {
			t.Fatal("post-precheck parent FIFO did not fail directory traversal", e)
		}
		return
	}
	private, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(private, 0700) != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(private, "trust-parent-fixture"), []byte(fixtureMarker), 0600); e != nil {
		t.Fatal(e)
	}
	parent := filepath.Join(private, "synthetic-parent")
	if e := os.Mkdir(parent, 0700); e != nil {
		t.Fatal(e)
	}
	stable, e := openTrustDirectory(parent)
	if e != nil {
		t.Fatal("stable trust directory refused", e)
	}
	if e := stable.Close(); e != nil {
		t.Fatal("stable trust directory close failed", e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestHealthProbeTrustParentOpenRefusesPostcheckFIFO$", "-test.timeout=2s")
	command.Env = append(os.Environ(), childInput+"="+parent)
	output, e := command.CombinedOutput()
	if e != nil || ctx.Err() != nil {
		t.Fatalf("owned parent FIFO refusal failed or hung; child joined: %v, %s", e, output)
	}
}

func TestHealthProbeTrustSnapshotIntegrity(t *testing.T) {
	_, raw := syntheticCertificate(t, []string{"portal.example.invalid"}, nil, false)
	for _, name := range []string{"unchanged", "replace_name", "replace_parent", "parent_symlink", "append", "rewrite_restore_mtime", "permissions", "parent_permissions", "new_hardlink"} {
		t.Run(name, func(t *testing.T) {
			path := syntheticTrustPath(t, raw)
			snapshot, e := openTrustSnapshot(path)
			if e != nil {
				t.Fatal(e)
			}
			defer snapshot.close()
			switch name {
			case "replace_name":
				if e = os.Rename(path, path+".original"); e == nil {
					e = os.WriteFile(path, raw, 0600)
				}
			case "replace_parent":
				parent := filepath.Dir(path)
				if e = os.Rename(parent, parent+"-original"); e == nil {
					e = os.Mkdir(parent, 0700)
				}
				if e == nil {
					e = os.WriteFile(path, raw, 0600)
				}
			case "parent_symlink":
				parent := filepath.Dir(path)
				if e = os.Rename(parent, parent+"-original"); e == nil {
					e = os.Symlink(parent+"-original", parent)
				}
			case "append":
				var file *os.File
				file, e = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if e == nil {
					_, e = file.Write([]byte(" "))
					closeError := file.Close()
					if e == nil {
						e = closeError
					}
				}
			case "rewrite_restore_mtime":
				// Change real bytes in the pinned inode, then restore its mtime.
				// The independent ctime snapshot must still reject the read.
				time.Sleep(time.Millisecond)
				changed := append([]byte(nil), raw...)
				changed[len(changed)/2] ^= 1
				e = os.WriteFile(path, changed, 0600)
				if e == nil {
					e = os.Chtimes(path, time.Now(), snapshot.before.ModTime())
				}
			case "permissions":
				e = os.Chmod(path, 0644)
			case "parent_permissions":
				e = os.Chmod(filepath.Dir(path), 0755)
			case "new_hardlink":
				e = os.Link(path, path+".second-link")
			}
			if e != nil {
				t.Fatal("owned synthetic mutation prerequisite", e)
			}
			actual, e := snapshot.read()
			if name == "unchanged" {
				if e != nil || !bytes.Equal(actual, raw) {
					t.Fatal("unchanged descriptor snapshot was not retained", e)
				}
			} else {
				healthFailure(t, e, trustInvalid)
			}
		})
	}
}
