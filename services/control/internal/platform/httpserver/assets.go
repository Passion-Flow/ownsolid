package httpserver

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Assets(w http.ResponseWriter, r *http.Request, root string) bool {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		path := filepath.Join(root, filepath.Clean(r.URL.Path))
		relative, e := filepath.Rel(root, path)
		if e != nil || strings.HasPrefix(relative, "..") {
			return false
		}
		st, e := os.Stat(path)
		if e != nil || !st.Mode().IsRegular() {
			return false
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, r, path)
		return true
	}
	http.ServeFile(w, r, filepath.Join(root, "index.html"))
	return true
}
