package engine

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

// These Unix HTTP servers are explicit Engine protocol fixtures, not real Docker.
func fixtureClient(t *testing.T, handler http.HandlerFunc) (*Client, *atomic.Int32) {
	t.Helper()
	// Keep the Unix address below macOS sockaddr_un's limit even on the
	// inventoried private APFS mount; never move sockets to another filesystem.
	directory, e := os.MkdirTemp(os.Getenv("TMPDIR"), "e")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(directory) })
	observeTemporaryEngineStorage(t, directory)
	socket := filepath.Join(directory, "s")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	calls := &atomic.Int32{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); handler(w, r) })}
	go server.Serve(listener)
	c, e := New(configuration.Engine{SocketFile: socket, Profile: "local-macos-amd64-emulation", DialSeconds: 1, RequestSeconds: 2, LoadSeconds: 5, ResponseBytes: 4096, MaximumExpandedImageBytes: 67108864})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close(); server.Close(); listener.Close(); os.Remove(socket); os.Remove(directory) })
	return c, calls
}
func versionJSON() string {
	return `{"Version":"29.8.2","ApiVersion":"1.56","MinAPIVersion":"1.40","Os":"linux","Arch":"arm64","future":null}`
}
func infoJSON() string {
	return `{"ID":"synthetic-engine-id","OSType":"linux","Architecture":"aarch64","ServerVersion":"29.8.2","DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`
}
func TestUnixObservationRejectsProxyAndUsesPinnedAPI(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://unreachable.invalid")
	t.Setenv("HTTPS_PROXY", "http://unreachable.invalid")
	t.Setenv("DOCKER_HOST", "tcp://unreachable.invalid")
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			io.WriteString(w, versionJSON())
		case "/v1.49/info":
			io.WriteString(w, infoJSON())
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	result, e := c.observe(context.Background())
	if e != nil || result.EngineVersion != "29.8.2" || result.DeclaredPlatform != "linux/amd64" || result.HostArchitecture != "arm64" || len(result.EngineSHA256) != 64 || calls.Load() != 2 {
		t.Fatal(result, e, calls.Load())
	}
}
func TestEngineRefusesWrongVersionAPIStoreAndMalformedReplies(t *testing.T) {
	for _, fault := range []string{"version", "api-minimum", "api-malformed", "store", "identity", "duplicate", "redirect", "response-limit", "http-error"} {
		t.Run(fault, func(t *testing.T) {
			c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				v := versionJSON()
				info := infoJSON()
				switch fault {
				case "version":
					v = strings.Replace(v, "29.8.2", "29.7.0", 1)
				case "api-minimum":
					v = strings.Replace(v, "1.40", "1.50", 1)
				case "api-malformed":
					v = strings.Replace(v, "1.56", "unknown", 1)
				case "store":
					info = strings.Replace(info, "io.containerd.snapshotter.v1", "legacy", 1)
				case "identity":
					info = strings.Replace(info, "aarch64", "amd64", 1)
				case "duplicate":
					v = strings.Replace(v, `"Os":"linux"`, `"Os":"linux","Os":"linux"`, 1)
				case "redirect":
					http.Redirect(w, r, "http://never-resolve.invalid", 307)
					return
				case "response-limit":
					io.WriteString(w, strings.Repeat("x", 5000))
					return
				case "http-error":
					w.WriteHeader(500)
					return
				}
				if r.URL.Path == "/version" {
					io.WriteString(w, v)
				} else {
					io.WriteString(w, info)
				}
			})
			if _, e := c.observe(context.Background()); e == nil {
				t.Fatal("invalid engine admitted")
			}
			if calls.Load() > 2 {
				t.Fatal("automatic retry/redirect occurred", calls.Load())
			}
		})
	}
}
func TestImageInspectionRequiresExactImmutablePlatform(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	for _, fault := range []string{"valid", "missing", "wrong-id", "wrong-arch", "wrong-descriptor", "wrong-rootfs"} {
		t.Run(fault, func(t *testing.T) {
			c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, id) {
					t.Error("unbound image lookup")
				}
				if fault == "missing" {
					w.WriteHeader(404)
					return
				}
				body := map[string]any{"Id": id, "Os": "linux", "Architecture": "amd64", "RepoTags": []string{}, "RootFS": map[string]any{"Type": "layers", "Layers": []string{"sha256:" + strings.Repeat("b", 64)}}, "Descriptor": map[string]any{"digest": id}}
				switch fault {
				case "wrong-id":
					body["Id"] = "sha256:" + strings.Repeat("c", 64)
				case "wrong-arch":
					body["Architecture"] = "arm64"
				case "wrong-descriptor":
					body["Descriptor"] = map[string]any{"digest": "sha256:" + strings.Repeat("d", 64)}
				case "wrong-rootfs":
					body["RootFS"] = map[string]any{"Type": "layers", "Layers": []string{"invalid"}}
				}
				json.NewEncoder(w).Encode(body)
			})
			image, found, e := c.Inspect(context.Background(), id)
			if fault == "valid" && (e != nil || !found || image.ID != id) {
				t.Fatal(image, found, e)
			}
			if fault == "missing" && (e != nil || found) {
				t.Fatal(found, e)
			}
			if fault != "valid" && fault != "missing" && e == nil {
				t.Fatal("wrong immutable identity accepted")
			}
		})
	}
}
func TestLoadHTTP200ErrorsAndCancelledRequestNeverSucceed(t *testing.T) {
	for _, fault := range []string{"valid", "error", "detail", "empty", "invalid", "http", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1.49/images/load" || r.URL.Query().Get("quiet") != "1" || r.URL.Query().Get("platform") != `{"os":"linux","architecture":"amd64"}` || r.Header.Get("Content-Type") != "application/x-tar" {
					t.Error("wrong official load request")
				}
				raw, _ := io.ReadAll(r.Body)
				if string(raw) != "synthetic tagless tar stream" {
					t.Error("wrong body")
				}
				switch fault {
				case "error":
					io.WriteString(w, `{"error":"explicit synthetic error"}`)
				case "detail":
					io.WriteString(w, `{"errorDetail":{"message":"explicit synthetic error"}}`)
				case "empty":
				case "invalid":
					io.WriteString(w, "invalid")
				case "http":
					w.WriteHeader(500)
				default:
					io.WriteString(w, `{"stream":"Loaded image ID: sha256:synthetic"}`+"\n")
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			e := c.LoadTagless(ctx, strings.NewReader("synthetic tagless tar stream"))
			if fault == "valid" && e != nil {
				t.Fatal(e)
			}
			if fault != "valid" && e == nil {
				t.Fatal("failed load returned success")
			}
			if calls.Load() > 1 {
				t.Fatal("non-idempotent load retried")
			}
		})
	}
}
func TestNativeProfileDoesNotAcceptMacOrARMFixture(t *testing.T) {
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			io.WriteString(w, versionJSON())
		} else {
			io.WriteString(w, infoJSON())
		}
	})
	c.config.Profile = c.policy.NativeProfile
	if _, e := c.Preflight(context.Background()); e == nil {
		t.Fatal("ARM/mac protocol fixture misqualified native amd64")
	}
}
