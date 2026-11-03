// Package objectstore implements the configured private S3 data plane. Current
// user/Workspace/object authorization belongs to the borrowing artifact owner.
package objectstore

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
)

const SeaweedFSCandidateVersion = "4.48"
const S3SDKVersion = "v1.114.3"
const MaximumMultipartParts = 10000

// Ref contains server-resolved UUIDs, never a supplied path or arbitrary prefix.
type Ref struct{ Tenant, Workspace, Object string }

func (r Ref) Key() (string, error) {
	for _, id := range []string{r.Tenant, r.Workspace, r.Object} {
		u, e := uuid.Parse(id)
		if e != nil || u == uuid.Nil || u.String() != id {
			return "", failure("OBJECT_SCOPE_INVALID")
		}
	}
	return "artifacts/" + r.Tenant + "/" + r.Workspace + "/" + r.Object + "/content", nil
}
func ValidDigest(v string) bool {
	b, e := hex.DecodeString(v)
	return e == nil && len(b) == 32 && strings.ToLower(v) == v
}
func failure(code string) error { return errors.New(code) }

type Part struct {
	Number int32  `json:"part_number"`
	Bytes  int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
	ETag   string `json:"etag"`
}
type Info struct {
	Bytes    int64
	ETag     string
	Metadata map[string]string
}
type Upload struct{ ID string }
type Download struct {
	Body io.ReadCloser
	Info Info
}

// Transport calls always name one exact object and upload. It never grants user
// authority, changes bucket configuration or performs a prefix-wide deletion.
type Transport interface {
	Identity() string
	VerifyProfile(context.Context) error
	PrivateObject(context.Context, Ref) error
	Create(context.Context, Ref, map[string]string) (Upload, error)
	FindUploads(context.Context, Ref) ([]Upload, error)
	PutPart(context.Context, Ref, string, Part, io.ReadSeeker) (Part, error)
	Parts(context.Context, Ref, string) ([]Part, error)
	Complete(context.Context, Ref, string, []Part) (Info, error)
	Abort(context.Context, Ref, string) error
	Head(context.Context, Ref) (Info, error)
	Open(context.Context, Ref, string) (Download, error)
	Delete(context.Context, Ref, string) error
	Close() error
}
