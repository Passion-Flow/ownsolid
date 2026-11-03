package httpserver

import "os"

// Download carries an already authorized, verified immutable artifact, never a caller path.
type Download struct {
	File        *os.File
	Size        int64
	SHA256      string
	Offset      int64
	FileName    string
	ContentType string
}

// MemoryDownload carries bounded manufacturer-generated metadata through the same authorized attachment response.
type MemoryDownload struct {
	Bytes       []byte
	SHA256      string
	FileName    string
	ContentType string
}
