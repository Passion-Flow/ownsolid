package httpserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const script = "console.log('the same words, again and again');\n"

func buildRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "assets"), 0o755); e != nil {
		t.Fatal(e)
	}
	var zipped bytes.Buffer
	writer := gzip.NewWriter(&zipped)
	if _, e := writer.Write([]byte(script)); e != nil || writer.Close() != nil {
		t.Fatal("gzip")
	}
	for name, body := range map[string][]byte{
		"index.html":              []byte("<!doctype html><title>page</title>"),
		"index.html.br":           []byte("brotli page"),
		"assets/app.js":           []byte(script),
		"assets/app.js.br":        []byte("brotli bytes"),
		"assets/app.js.gz":        zipped.Bytes(),
		"assets/only-gzip.css":    []byte("body{}"),
		"assets/only-gzip.css.gz": zipped.Bytes(),
		"assets/font.woff2":       []byte("font"),
	} {
		if e := os.WriteFile(filepath.Join(root, name), body, 0o644); e != nil {
			t.Fatal(e)
		}
	}
	return root
}

func get(t *testing.T, root, path, accept string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	if !Assets(w, r, root) {
		w.Code = http.StatusNotFound
	}
	return w
}

func TestAssetsSendThePrecompressedCopyTheBrowserAccepts(t *testing.T) {
	root := buildRoot(t)
	for _, c := range []struct {
		name, path, accept, encoding, body string
	}{
		{"brotli first", "/assets/app.js", "gzip, deflate, br, zstd", "br", "brotli bytes"},
		{"gzip when brotli is not accepted", "/assets/app.js", "gzip", "gzip", ""},
		{"brotli refused with q=0", "/assets/app.js", "br;q=0, gzip", "gzip", ""},
		{"nothing accepted", "/assets/app.js", "", "", script},
		{"identity only", "/assets/app.js", "identity", "", script},
		{"wildcard", "/assets/app.js", "*", "br", "brotli bytes"},
		{"wildcard without what it excludes", "/assets/app.js", "br;q=0, *", "gzip", ""},
		{"everything refused", "/assets/app.js", "*;q=0", "", script},
		{"a value that does not parse", "/assets/app.js", "br;q=high", "", script},
		{"case and spaces", "/assets/app.js", " GZIP ; Q=1 ", "gzip", ""},
		{"only the copy that exists", "/assets/only-gzip.css", "br, gzip", "gzip", ""},
		{"no copy at all", "/assets/font.woff2", "br, gzip", "", "font"},
		{"the page itself", "/nodes/abc", "br", "br", "brotli page"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := get(t, root, c.path, c.accept)
			if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != c.encoding {
				t.Fatalf("status %d, encoding %q", w.Code, w.Header().Get("Content-Encoding"))
			}
			if w.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatal("caches must key on the encoding", w.Header())
			}
			if c.body != "" && w.Body.String() != c.body {
				t.Fatalf("body %q", w.Body.String())
			}
			if c.encoding == "gzip" {
				reader, e := gzip.NewReader(w.Body)
				if e != nil {
					t.Fatal(e)
				}
				if plain, _ := io.ReadAll(reader); string(plain) != script {
					t.Fatal("gzip copy decodes to the file")
				}
			}
		})
	}
}

func TestAssetsKeepTheFilesTypeAndCaching(t *testing.T) {
	root := buildRoot(t)
	w := get(t, root, "/assets/app.js", "br")
	if w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatal("type of the file, not of the copy", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal("hashed assets are cached for good")
	}
	page := get(t, root, "/", "br")
	if page.Header().Get("Content-Type") != "text/html; charset=utf-8" || page.Header().Get("Cache-Control") != "" {
		t.Fatal("the page keeps its type and is not cached for good", page.Header())
	}
}

func TestAssetsRefuseWhatIsNotAFile(t *testing.T) {
	root := buildRoot(t)
	if e := os.Mkdir(filepath.Join(root, "assets", "folder"), 0o755); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/assets/missing.js", "/assets/folder", "/assets/../../etc/passwd"} {
		if w := get(t, root, path, "br"); w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d", path, w.Code)
		}
	}
	// A copy alone does not make a file: the original must exist.
	if e := os.Remove(filepath.Join(root, "assets", "app.js")); e != nil {
		t.Fatal(e)
	}
	if w := get(t, root, "/assets/app.js", "br"); w.Code != http.StatusNotFound {
		t.Fatal("served a copy without its file")
	}
}

func TestAssetsAnswerConditionalAndRangeRequests(t *testing.T) {
	root := buildRoot(t)
	first := get(t, root, "/assets/app.js", "br")
	modified := first.Header().Get("Last-Modified")
	if modified == "" {
		t.Fatal("no Last-Modified")
	}
	if w := get(t, root, "/assets/app.js", "br", "If-Modified-Since", modified); w.Code != http.StatusNotModified {
		t.Fatal("conditional request", w.Code)
	}
	w := get(t, root, "/assets/app.js", "br", "Range", "bytes=0-5")
	if w.Code != http.StatusPartialContent || w.Body.String() != "brotli" || w.Header().Get("Content-Encoding") != "br" {
		t.Fatal("ranges apply to the copy that is sent", w.Code, w.Body.String())
	}
}
