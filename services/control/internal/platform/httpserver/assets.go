package httpserver

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// precompressed lists the copies the web build writes next to its files, best first.
var precompressed = []struct{ coding, suffix string }{{"br", ".br"}, {"gzip", ".gz"}}

// Assets serves an application's build from root: a file under /assets/, or index.html
// for any other path so the application's router can answer it. Hashed files under /assets/
// are cached for good; the page is not. When the browser accepts it, the build's Brotli or
// gzip copy goes out in place of the file, so nothing is compressed on a request (UX-121).
// It returns false when an /assets/ path names no file.
func Assets(w http.ResponseWriter, r *http.Request, root string) bool {
	path := filepath.Join(root, "index.html")
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		path = filepath.Join(root, filepath.Clean(r.URL.Path))
		relative, e := filepath.Rel(root, path)
		if e != nil || strings.HasPrefix(relative, "..") {
			return false
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	file, info, ok := open(path)
	if !ok {
		return false
	}
	defer file.Close()
	if kind := mime.TypeByExtension(filepath.Ext(path)); kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	w.Header().Add("Vary", "Accept-Encoding")
	accepted := acceptedCodings(r.Header.Values("Accept-Encoding"))
	for _, copy := range precompressed {
		if !accepted[copy.coding] {
			continue
		}
		encoded, encodedInfo, ok := open(path + copy.suffix)
		if !ok {
			continue
		}
		defer encoded.Close()
		w.Header().Set("Content-Encoding", copy.coding)
		http.ServeContent(w, r, "", encodedInfo.ModTime(), encoded)
		return true
	}
	http.ServeContent(w, r, "", info.ModTime(), file)
	return true
}

func open(path string) (*os.File, os.FileInfo, bool) {
	file, e := os.Open(path)
	if e != nil {
		return nil, nil, false
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, false
	}
	return file, info, true
}

// acceptedCodings reads Accept-Encoding (RFC 9110, section 12.5.3) for the codings the build
// provides. A coding named with q=0 is refused, `*` stands for any coding not named, and a
// value that does not parse counts as not accepted, which only means the plain file.
func acceptedCodings(values []string) map[string]bool {
	named := map[string]bool{}
	accepted := map[string]bool{}
	wildcard := false
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			coding, weight, ok := codingWeight(item)
			if !ok {
				continue
			}
			if coding == "*" {
				wildcard = weight > 0
				continue
			}
			named[coding] = true
			accepted[coding] = weight > 0
		}
	}
	for _, copy := range precompressed {
		if !named[copy.coding] {
			accepted[copy.coding] = wildcard
		}
	}
	return accepted
}

func codingWeight(item string) (string, float64, bool) {
	parts := strings.Split(item, ";")
	coding := strings.ToLower(strings.TrimSpace(parts[0]))
	if coding == "" {
		return "", 0, false
	}
	weight := 1.0
	for _, parameter := range parts[1:] {
		name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		q, e := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if e != nil || q < 0 || q > 1 {
			return "", 0, false
		}
		weight = q
	}
	return coding, weight, true
}
