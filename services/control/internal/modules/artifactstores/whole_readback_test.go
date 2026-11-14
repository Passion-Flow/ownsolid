package artifactstores

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// This controlled transport represents a one-slot profile: an open GET owns
// that slot until Close. The real private-S3 case exercises the native adapter.
type wholeReadbackTransport struct {
	objectstore.Transport
	source     []byte
	info       objectstore.Info
	held       bool
	closes     int
	heads      int
	private    int
	closeError error
}

func (t *wholeReadbackTransport) Head(context.Context, objectstore.Ref) (objectstore.Info, error) {
	if t.held {
		return objectstore.Info{}, errors.New("OBJECT_SLOT_HELD")
	}
	t.heads++
	return t.info, nil
}
func (t *wholeReadbackTransport) Open(context.Context, objectstore.Ref, string) (objectstore.Download, error) {
	t.held = true
	return objectstore.Download{Body: &wholeReadbackBody{Reader: bytes.NewReader(t.source), owner: t}, Info: t.info}, nil
}
func (t *wholeReadbackTransport) PrivateObject(context.Context, objectstore.Ref) error {
	if t.held {
		return errors.New("OBJECT_SLOT_HELD")
	}
	t.private++
	return nil
}

type wholeReadbackBody struct {
	io.Reader
	owner *wholeReadbackTransport
}

func (b *wholeReadbackBody) Close() error {
	b.owner.closes++
	b.owner.held = false
	return b.owner.closeError
}
func TestWholeReadbackClosesBeforeFurtherRemoteObservations(t *testing.T) {
	for _, mode := range []string{"accepted", "digest_mismatch", "close_failure"} {
		t.Run(mode, func(t *testing.T) {
			source := []byte("Exact whole-source bytes")
			sum := sha256.Sum256(source)
			o := storedObject{Object: Object{ID: "11111111-1111-4111-8111-111111111111", ExpectedBytes: int64(len(source)), SHA256: hex.EncodeToString(sum[:])}}
			transport := &wholeReadbackTransport{source: source, info: objectstore.Info{Bytes: o.ExpectedBytes, ETag: `"opaque"`, Metadata: metadata(o)}}
			if mode == "digest_mismatch" {
				transport.source = bytes.Repeat([]byte{'x'}, len(source))
			}
			if mode == "close_failure" {
				transport.closeError = errors.New("sensitive provider failure")
			}
			service := &Service{Transport: transport}
			out, err := service.verifyWhole(context.Background(), o)
			if transport.held || transport.closes != 1 {
				t.Fatal("GET reservation was leaked or closed repeatedly", transport.closes)
			}
			if mode == "accepted" {
				if err != nil || !reflect.DeepEqual(out, transport.info) || transport.heads != 2 || transport.private != 1 {
					t.Fatal("whole-source follow-up observations missing", out, err, transport.heads, transport.private)
				}
			} else if err == nil || !reflect.DeepEqual(out, objectstore.Info{}) || transport.heads != 1 || transport.private != 0 {
				t.Fatal("failed read/close published info or continued remote observations", out, err)
			}
		})
	}
}
