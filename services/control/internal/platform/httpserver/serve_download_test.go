package httpserver

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAuthorizedDownloadConsumesDescriptorAndExactRange(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "object")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteString("prefix-content-tail"); e != nil {
		t.Fatal(e)
	}
	w := httptest.NewRecorder()
	e = ServeDownload(w, httptest.NewRequest("GET", "/content", nil), 200, Download{File: f, Offset: 7, Size: 7, FileName: "object.bin", SHA256: "retained"})
	if e != nil || w.Body.String() != "content" || w.Header().Get("Content-Length") != "7" {
		t.Fatal(e, w.Body.String())
	}
	if _, e = f.Stat(); e == nil {
		t.Fatal("descriptor left open")
	}
}
func TestDownloadCancelledAndInvalidScopeReleaseWithoutHeaders(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		f, e := os.CreateTemp(t.TempDir(), "object")
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if cancelled {
			cancel()
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/content", nil).WithContext(ctx)
		if e = ServeDownload(w, r, 200, Download{File: f, Size: 1}); e == nil || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "" {
			t.Fatal("invalid response", e)
		}
		if _, e = f.Stat(); e == nil {
			t.Fatal("descriptor left open")
		}
	}
}
