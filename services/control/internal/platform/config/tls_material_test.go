package config

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// All certificate and private-key bytes in these controls are generated
// synthetic inputs. No system trust store, account or deployment key is used.
func tlsMaterialTestPair(t *testing.T, certificateMode, keyMode os.FileMode) (string, string) {
	t.Helper()
	certificate, key := transportTLSFixture(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}, false)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publicParent, keyParent := filepath.Join(root, "public"), filepath.Join(root, "private")
	for parent, mode := range map[string]os.FileMode{publicParent: 0755, keyParent: 0700} {
		if err = os.Mkdir(parent, mode); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(parent, mode); err != nil {
			t.Fatal(err)
		}
	}
	certificatePath, keyPath := filepath.Join(publicParent, "certificate.pem"), filepath.Join(keyParent, "key.pem")
	if err = os.Rename(certificate, certificatePath); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(key, keyPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(certificatePath, certificateMode); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(keyPath, keyMode); err != nil {
		t.Fatal(err)
	}
	return certificatePath, keyPath
}

func tlsMaterialTestConfiguration(certificate, key string) Config {
	c := validConfig()
	c.Server = transportTestServer(TransportDirectHTTPS)
	c.Server.TLSCertificateFile, c.Server.TLSPrivateKeyFile = certificate, key
	return c
}

func tlsMaterialTestClose(t *testing.T, snapshot *tlsMaterialSnapshot) {
	t.Helper()
	file, root := snapshot.file, snapshot.root
	if err := snapshot.close(); err != nil {
		t.Error("TLS snapshot close failed", err)
	}
	if file != nil {
		if _, err := file.Stat(); err == nil {
			t.Error("closed TLS descriptor remained usable")
		}
	}
	if root != nil {
		if _, err := root.Stat("."); err == nil {
			t.Error("closed TLS parent remained usable")
		}
	}
	if snapshot.file != nil || snapshot.root != nil || snapshot.close() != nil {
		t.Error("snapshot retained a closed descriptor or failed idempotent close")
	}
}

func tlsMaterialTestPinPair(t *testing.T, certificatePath, keyPath string) (*tlsMaterialSnapshot, *tlsMaterialSnapshot) {
	t.Helper()
	key, err := pinTLSMaterial(keyPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tlsMaterialTestClose(t, key) })
	certificate, err := pinTLSMaterial(certificatePath, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tlsMaterialTestClose(t, certificate) })
	return certificate, key
}

func tlsMaterialTestRefusal(t *testing.T, certificate, key []byte, err error, private bool) {
	t.Helper()
	if certificate != nil || key != nil || err == nil || err.Error() != tlsMaterialError(private).Error() {
		t.Fatal("unsafe TLS pair was projected or error was not redacted", err)
	}
}

func TestTLSMaterialPinnedPublicAndPrivateSnapshots(t *testing.T) {
	for _, value := range []struct {
		name        string
		certificate os.FileMode
		key         os.FileMode
	}{
		{"certificate_0444_key_0400", 0444, 0400},
		{"certificate_0444_key_0600", 0444, 0600},
		{"certificate_0644_key_0400", 0644, 0400},
		{"certificate_0644_key_0600", 0644, 0600},
	} {
		t.Run(value.name, func(t *testing.T) {
			certificate, key := tlsMaterialTestPair(t, value.certificate, value.key)
			c := tlsMaterialTestConfiguration(certificate, key)
			got, err := c.TLSConfig()
			if err != nil || got == nil || len(got.Certificates) != 1 || got.MinVersion != tls.VersionTLS12 {
				t.Fatal("actual generated pair refused", err)
			}
			parent, err := os.Stat(filepath.Dir(certificate))
			if err != nil || parent.Mode().Perm() != 0755 {
				t.Fatal("public certificate directory policy changed", err)
			}
		})
	}
	for _, role := range []string{"certificate", "private_key"} {
		for _, kind := range []string{"relative", "unclean", "missing", "directory", "fifo", "empty", "oversized", "symlink_file", "symlink_parent", "hardlink", "group_write", "other_write", "execute", "setuid", "setgid", "sticky"} {
			t.Run(role+"_"+kind, func(t *testing.T) {
				certificate, key := tlsMaterialTestPair(t, 0644, 0600)
				private := role == "private_key"
				selected := certificate
				if private {
					selected = key
				}
				switch kind {
				case "relative":
					selected = filepath.Base(selected)
				case "unclean":
					selected = filepath.Dir(selected) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(selected)
				case "missing":
					selected += ".missing"
				case "directory", "fifo":
					if err := os.Remove(selected); err != nil {
						t.Fatal(err)
					}
					var err error
					if kind == "directory" {
						err = os.Mkdir(selected, 0700)
					} else {
						err = syscall.Mkfifo(selected, 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
				case "empty":
					if err := os.Truncate(selected, 0); err != nil {
						t.Fatal(err)
					}
				case "oversized":
					if err := os.Truncate(selected, maximumTLSMaterialBytes+1); err != nil {
						t.Fatal(err)
					}
				case "symlink_file":
					if err := os.Symlink(filepath.Base(selected), selected+".link"); err != nil {
						t.Fatal(err)
					}
					selected += ".link"
				case "symlink_parent":
					parent := filepath.Dir(selected)
					if err := os.Symlink(parent, parent+".link"); err != nil {
						t.Fatal(err)
					}
					selected = filepath.Join(parent+".link", filepath.Base(selected))
				case "hardlink":
					if err := os.Link(selected, selected+".unapproved"); err != nil {
						t.Fatal(err)
					}
				case "group_write", "other_write", "execute", "setuid", "setgid", "sticky":
					mode := map[string]os.FileMode{"group_write": 0620, "other_write": 0602, "execute": 0700, "setuid": 0600 | os.ModeSetuid, "setgid": 0600 | os.ModeSetgid, "sticky": 0600 | os.ModeSticky}[kind]
					if err := os.Chmod(selected, mode); err != nil {
						t.Fatal(err)
					}
				}
				if private {
					key = selected
				} else {
					certificate = selected
				}
				certPEM, keyPEM, err := readTLSMaterialPair(certificate, key)
				tlsMaterialTestRefusal(t, certPEM, keyPEM, err, private)
			})
		}
	}
	for _, mode := range []os.FileMode{0640, 0604} {
		name := "private_group_read"
		if mode == 0604 {
			name = "private_other_read"
		}
		t.Run(name, func(t *testing.T) {
			certificate, key := tlsMaterialTestPair(t, 0644, mode)
			got, err := tlsMaterialTestConfiguration(certificate, key).TLSConfig()
			if got != nil || err == nil || err.Error() != "SECRET_FILE_INVALID" {
				t.Fatal("readable private key accepted or error leaked", err)
			}
		})
	}
	t.Run("same_file_alias_refused", func(t *testing.T) {
		_, key := tlsMaterialTestPair(t, 0644, 0600)
		certificatePEM, keyPEM, err := readTLSMaterialPair(key, key)
		tlsMaterialTestRefusal(t, certificatePEM, keyPEM, err, false)
	})
	t.Run("maximum_raw_snapshot_boundary", func(t *testing.T) {
		certificate, key := tlsMaterialTestPair(t, 0644, 0600)
		certBytes, keyBytes := bytes.Repeat([]byte("c"), int(maximumTLSMaterialBytes)), bytes.Repeat([]byte("k"), int(maximumTLSMaterialBytes))
		if err := os.WriteFile(certificate, certBytes, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(key, keyBytes, 0600); err != nil {
			t.Fatal(err)
		}
		// These are deliberately not PEM. This controls bounded raw admission,
		// not a claim that arbitrary bytes form a valid certificate or key.
		certPEM, keyPEM, err := readTLSMaterialPair(certificate, key)
		if err != nil || !bytes.Equal(certPEM, certBytes) || !bytes.Equal(keyPEM, keyBytes) {
			t.Fatal("exact material wire bound refused or changed", err)
		}
	})
	t.Run("malformed_pair_fixed_error", func(t *testing.T) {
		certificate, key := tlsMaterialTestPair(t, 0644, 0600)
		if err := os.WriteFile(certificate, []byte("synthetic invalid certificate"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := tlsMaterialTestConfiguration(certificate, key).TLSConfig()
		if got != nil || err == nil || err.Error() != "TLS_KEYPAIR_INVALID" {
			t.Fatal("invalid PEM pair accepted or native error leaked", err)
		}
	})
	t.Run("foreign_owner_is_not_certificate_policy", func(t *testing.T) {
		certificate, _ := tlsMaterialTestPair(t, 0444, 0400)
		before, err := os.Lstat(certificate)
		if err != nil {
			t.Fatal(err)
		}
		facts, ok := before.Sys().(*syscall.Stat_t)
		if !ok || facts == nil {
			t.Fatal("native stat unavailable")
		}
		// Explicit synthetic facts control admission policy only. This does not
		// perform chown or claim actual cross-account filesystem qualification.
		foreign := *facts
		foreign.Uid++
		if !tlsMaterialAdmitted(tlsMaterialTestFacts{before, &foreign}, false) {
			t.Fatal("public certificate incorrectly required the runtime UID")
		}
	})
}

type tlsMaterialTestFacts struct {
	os.FileInfo
	native any
}

func (value tlsMaterialTestFacts) Sys() any { return value.native }

const tlsMaterialFIFOChildMarker = "PROJECT_ONE_TEST_TLS_MATERIAL_FIFO_CHILD"

func tlsMaterialTestPostcheckFIFO(t *testing.T, private bool) {
	t.Helper()
	certificatePath, keyPath := tlsMaterialTestPair(t, 0644, 0600)
	certificate, key := tlsMaterialTestPinPair(t, certificatePath, keyPath)
	selected, path := certificate, certificatePath
	if private {
		selected, path = key, keyPath
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := openPinnedTLSMaterial(selected); err == nil || err.Error() != tlsMaterialError(private).Error() || selected.file != nil {
		t.Fatal("actual postcheck FIFO accepted or descriptor escaped", err)
	}
}

func TestTLSMaterialRejectsActualReplacementsAndRestoredMtime(t *testing.T) {
	for _, role := range []string{"certificate", "private_key"} {
		t.Run(role+"_postcheck_fifo", func(t *testing.T) {
			if os.Getenv(tlsMaterialFIFOChildMarker) == role {
				tlsMaterialTestPostcheckFIFO(t, role == "private_key")
				t.Log("TLS_MATERIAL_ACTUAL_POSTCHECK_FIFO_REFUSED")
				return
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			childScratch, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(childScratch, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestTLSMaterialRejectsActualReplacementsAndRestoredMtime$/^"+role+"_postcheck_fifo$", "-test.count=1", "-test.timeout=3s", "-test.v")
			command.WaitDelay = time.Second
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, tlsMaterialFIFOChildMarker+"=") && !strings.HasPrefix(value, "TMPDIR=") {
					command.Env = append(command.Env, value)
				}
			}
			command.Env = append(command.Env, tlsMaterialFIFOChildMarker+"="+role, "TMPDIR="+childScratch)
			output, err := command.CombinedOutput()
			if err != nil || ctx.Err() != nil || command.ProcessState == nil || !command.ProcessState.Success() || bytes.Count(output, []byte("TLS_MATERIAL_ACTUAL_POSTCHECK_FIFO_REFUSED")) != 1 {
				t.Fatalf("TLS postcheck FIFO child failed or did not join: %v; %s", err, output)
			}
		})
		for _, kind := range []string{"regular", "symlink", "mode", "link", "parent", "parent_mode", "parent_sibling_directory", "parent_restored_mtime", "restored_mtime", "replacement_after_pair_read"} {
			t.Run(role+"_"+kind, func(t *testing.T) {
				certificatePath, keyPath := tlsMaterialTestPair(t, 0644, 0600)
				certificate, key := tlsMaterialTestPinPair(t, certificatePath, keyPath)
				selected, path, private := certificate, certificatePath, role == "private_key"
				if private {
					selected, path = key, keyPath
				}
				var selectedBody []byte
				if kind == "restored_mtime" || kind == "replacement_after_pair_read" {
					for _, snapshot := range []*tlsMaterialSnapshot{key, certificate} {
						if err := snapshot.open(); err != nil {
							t.Fatal(err)
						}
					}
					for _, snapshot := range []*tlsMaterialSnapshot{key, certificate} {
						raw, err := snapshot.read()
						if err != nil {
							t.Fatal(err)
						}
						if snapshot == selected {
							selectedBody = raw
						}
					}
				}
				var err error
				switch kind {
				case "regular", "symlink", "replacement_after_pair_read":
					if err = os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
					if kind == "symlink" {
						err = os.Symlink(filepath.Base(path)+".original", path)
					} else {
						err = os.WriteFile(path, bytes.Repeat([]byte("x"), int(selected.before.Size())), 0600)
					}
				case "mode":
					mode := os.FileMode(0664)
					if private {
						mode = 0644
					}
					err = os.Chmod(path, mode)
				case "link":
					err = os.Link(path, path+".unapproved")
				case "parent":
					parent := filepath.Dir(path)
					if err = os.Rename(parent, parent+".original"); err != nil {
						t.Fatal(err)
					}
					if err = os.Mkdir(parent, 0755); err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(path, bytes.Repeat([]byte("x"), int(selected.before.Size())), 0600); err != nil {
						t.Fatal(err)
					}
					pinned, statError := selected.root.Lstat(selected.name)
					if statError != nil || !os.SameFile(selected.before, pinned) {
						t.Fatal("pinned TLS parent switched to replacement", statError)
					}
				case "parent_mode":
					err = os.Chmod(filepath.Dir(path), selected.directory.Mode().Perm()^0010)
				case "parent_sibling_directory":
					err = os.Mkdir(filepath.Join(filepath.Dir(path), "unexpected-sibling"), 0700)
				case "parent_restored_mtime":
					parent := filepath.Dir(path)
					// Linux can give all these operations the same coarse-clock
					// ctime, despite exposing nanosecond stat fields. Establish an
					// observable native change before testing its rejection; never
					// skip the integrity assertions when the fixture cannot do so.
					deadline := time.Now().Add(time.Second)
					var after os.FileInfo
					for attempts := 1; ; attempts++ {
						if err = os.Chmod(parent, selected.directory.Mode().Perm()^0010); err != nil {
							t.Fatal(err)
						}
						if err = os.Chmod(parent, selected.directory.Mode().Perm()); err != nil {
							t.Fatal(err)
						}
						if err = os.Chtimes(parent, selected.directory.ModTime(), selected.directory.ModTime()); err != nil {
							t.Fatal(err)
						}
						var statError error
						after, statError = os.Lstat(parent)
						if statError != nil || !os.SameFile(selected.directory, after) || selected.directory.Mode() != after.Mode() || selected.directory.Size() != after.Size() || !selected.directory.ModTime().Equal(after.ModTime()) {
							t.Fatal("parent rewrite did not restore original inode/mode/size/mtime facts", statError)
						}
						if !samePrivateChangeTime(selected.directory, after) {
							t.Logf("parent native change-time mutation established after %d attempt(s)", attempts)
							break
						}
						if !time.Now().Before(deadline) {
							t.Fatal("parent rewrite did not establish a native change-time mutation before fixture deadline")
						}
						time.Sleep(time.Millisecond)
					}
					if samePrivateChangeTime(selected.directory, after) || sameTLSParentSnapshot(selected.directory, after) {
						t.Fatal("restored parent mtime concealed native change-time mutation")
					}
				case "restored_mtime":
					writer, openError := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0)
					if openError != nil {
						t.Fatal(openError)
					}
					body := bytes.Repeat([]byte("z"), int(selected.before.Size()))
					written, writeError := writer.Write(body)
					syncError, closeError := writer.Sync(), writer.Close()
					if writeError != nil || written != len(body) || syncError != nil || closeError != nil {
						t.Fatal("controlled actual rewrite failed", writeError, syncError, closeError)
					}
					if err = os.Chtimes(path, selected.before.ModTime(), selected.before.ModTime()); err != nil {
						t.Fatal(err)
					}
					after, statError := selected.root.Lstat(selected.name)
					if statError != nil || !os.SameFile(selected.before, after) || selected.before.Size() != after.Size() || !selected.before.ModTime().Equal(after.ModTime()) {
						t.Fatal("rewrite did not establish same inode/size and restored mtime", statError)
					}
					if samePrivateChangeTime(selected.before, after) || sameTLSMaterialSnapshot(selected.before, after, private) {
						t.Fatal("restored mtime concealed a native change-time mutation")
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				if kind == "restored_mtime" || kind == "replacement_after_pair_read" {
					err = selected.validate()
				} else {
					err = openPinnedTLSMaterial(selected)
					if selected.file != nil {
						t.Fatal("failed TLS open exposed a descriptor")
					}
				}
				if err == nil || err.Error() != tlsMaterialError(private).Error() {
					t.Fatal("actual replacement or rewrite accepted or error leaked", err)
				}
				if kind == "restored_mtime" || kind == "replacement_after_pair_read" {
					if kind == "replacement_after_pair_read" {
						if err = os.Remove(path); err != nil {
							t.Fatal(err)
						}
						err = os.Rename(path+".original", path)
					} else {
						err = os.WriteFile(path, selectedBody, 0600)
					}
					if err != nil {
						t.Fatal(err)
					}
					got, retryError := tlsMaterialTestConfiguration(certificatePath, keyPath).TLSConfig()
					if retryError != nil || got == nil {
						t.Fatal("explicit correction and fresh pair retry failed", retryError)
					}
				}
			})
		}
	}
	t.Run("missing_native_facts_refuse", func(t *testing.T) {
		certificate, _ := tlsMaterialTestPair(t, 0644, 0600)
		before, err := os.Lstat(certificate)
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range []os.FileInfo{nil, tlsMaterialTestFacts{before, nil}, tlsMaterialTestFacts{before, (*syscall.Stat_t)(nil)}} {
			if tlsMaterialAdmitted(info, false) || sameTLSMaterialSnapshot(before, info, false) || sameTLSMaterialSnapshot(info, before, false) {
				t.Fatal("missing native TLS facts accepted")
			}
		}
		parent, err := os.Lstat(filepath.Dir(certificate))
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range []os.FileInfo{nil, tlsMaterialTestFacts{parent, nil}, tlsMaterialTestFacts{parent, (*syscall.Stat_t)(nil)}} {
			if sameTLSParentSnapshot(parent, info) || sameTLSParentSnapshot(info, parent) {
				t.Fatal("missing native TLS parent facts accepted")
			}
		}
	})
}
