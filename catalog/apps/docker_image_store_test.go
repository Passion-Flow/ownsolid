package apps

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const dockerTestID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func dockerSocketFixture(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "oci-socket-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "d")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	return socket
}

func TestLocalDockerImageStoreLoadsAndReadsTheManifest(t *testing.T) {
	const transport = "verified tagless tar bytes"
	var loaded atomic.Bool
	socket := dockerSocketFixture(t, func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1.49/info":
			_, _ = io.WriteString(response, `{"DriverStatus":[["driver-type","io.containerd.snapshotter.v1"]]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v1.49/images/"+dockerTestID+"/json":
			if request.URL.Query().Get("platform") != dockerLinuxAMD64 {
				t.Error("readback did not select Linux amd64")
			}
			if !loaded.Load() {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(response, `{"Id":%q,"Os":"linux","Architecture":"amd64","Descriptor":{"digest":%q,"mediaType":%q}}`,
				dockerTestID, dockerTestID, ociManifestType)
		case request.Method == http.MethodPost && request.URL.Path == "/v1.49/images/load":
			body, err := io.ReadAll(request.Body)
			if err != nil || string(body) != transport || request.Header.Get("Content-Type") != "application/x-tar" || request.URL.Query().Get("quiet") != "1" {
				t.Errorf("invalid local load request: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			loaded.Store(true)
			_, _ = io.WriteString(response, `{"stream":"accepted"}`+"\n")
		default:
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
			response.WriteHeader(http.StatusBadRequest)
		}
	})
	store, err := newDockerImageStore(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer store.client.CloseIdleConnections()
	ctx := t.Context()
	if err := store.requireContainerd(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := store.inspect(ctx, dockerTestID); err != nil || found {
		t.Fatalf("missing image returned %t, %v", found, err)
	}
	if err := store.load(ctx, func(w io.Writer) error {
		_, err := io.WriteString(w, transport)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if found, err := store.inspect(ctx, dockerTestID); err != nil || !found {
		t.Fatalf("imported image returned %t, %v", found, err)
	}
}

func TestLocalDockerImageStoreRefusesUnconfirmedEffects(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		writeErr    error
	}{
		{name: "progress error", reply: `{"stream":"accepted"} {"errorDetail":{"message":"load failed"}}`},
		{name: "malformed progress", reply: `{"stream":"accepted"} {`},
		{name: "oversized response", reply: strings.Repeat(" ", dockerResponseMax+1)},
		{name: "partial input", reply: `{"stream":"accepted"}`, writeErr: errors.New("source changed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := dockerSocketFixture(t, func(response http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				_, _ = io.WriteString(response, tc.reply)
			})
			store, err := newDockerImageStore(socket)
			if err != nil {
				t.Fatal(err)
			}
			defer store.client.CloseIdleConnections()
			if err := store.load(t.Context(), func(output io.Writer) error {
				_, err := io.WriteString(output, "archive prefix")
				if err != nil {
					return err
				}
				return tc.writeErr
			}); !errors.Is(err, ErrImageImport) {
				t.Fatalf("unconfirmed effect was accepted: %v", err)
			}
		})
	}
}

func TestLocalDockerImageStoreRefusesRedirectAndIdentityMismatch(t *testing.T) {
	socket := dockerSocketFixture(t, func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1.49/info":
			http.Redirect(response, request, "http://example.invalid/redirected", http.StatusFound)
		case "/v1.49/images/" + dockerTestID + "/json":
			_, _ = fmt.Fprintf(response, `{"Id":%q,"Os":"linux","Architecture":"amd64","Descriptor":{"digest":%q,"mediaType":%q}}`,
				dockerTestID, "sha256:"+strings.Repeat("b", 64), ociManifestType)
		default:
			t.Errorf("followed an unexpected path %s", request.URL.Path)
			response.WriteHeader(http.StatusBadRequest)
		}
	})
	store, err := newDockerImageStore(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer store.client.CloseIdleConnections()
	if err := store.requireContainerd(t.Context()); !errors.Is(err, ErrImageImport) {
		t.Fatalf("redirected engine was accepted: %v", err)
	}
	if found, err := store.inspect(t.Context(), dockerTestID); found || !errors.Is(err, ErrImageImport) {
		t.Fatalf("mismatched readback returned %t, %v", found, err)
	}
	if _, err := newDockerImageStore("tcp://example.invalid:2375"); !errors.Is(err, ErrImageImport) {
		t.Fatalf("TCP endpoint was accepted: %v", err)
	}
}

func TestImportInstallImageRefusesUnsignedDataBeforeEngineAccess(t *testing.T) {
	image := newImageFixture(t, "amd64", false)
	fixture := signOCI(t, ociTar(t, image.files, ""), image.image)
	socket := dockerSocketFixture(t, func(response http.ResponseWriter, request *http.Request) {
		t.Error("unverified bundle contacted the engine")
		response.WriteHeader(http.StatusBadRequest)
	})
	wrongKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportInstallImage(context.Background(), fixture.root.Name(), wrongKey, ImageImportOptions{
		DockerSocket: socket, MaximumPayloadBytes: fixture.limit, MaximumExpandedBytes: 1 << 20, Timeout: time.Second,
	})
	if !errors.Is(err, ErrBundle) {
		t.Fatalf("wrong publisher key returned %v", err)
	}
}
