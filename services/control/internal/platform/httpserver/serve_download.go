package httpserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
)

// ServeDownload consumes an already authorized descriptor on every outcome.
// A transport failure after headers aborts the response, never appends success or JSON.
func ServeDownload(w http.ResponseWriter, r *http.Request, status int, d Download) error {
	if d.File == nil {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	defer d.File.Close()
	if r.Context().Err() != nil {
		return errors.New("OBJECT_CANCELLED")
	}
	st, e := d.File.Stat()
	if e != nil || !st.Mode().IsRegular() || d.Offset < 0 || d.Size < 0 || d.Offset > st.Size() || d.Size > st.Size()-d.Offset || status != http.StatusOK {
		return errors.New("STORAGE_UNAVAILABLE")
	}
	contentType := d.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(d.Size, 10))
	w.Header().Set("X-Artifact-SHA256", d.SHA256)
	if d.FileName != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.FileName}))
	}
	stop := context.AfterFunc(r.Context(), func() { _ = d.File.Close() })
	defer stop()
	w.WriteHeader(status)
	if _, e = io.CopyN(w, io.NewSectionReader(d.File, d.Offset, d.Size), d.Size); e != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
