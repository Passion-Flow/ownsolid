package directory

import (
	"bytes"
	"errors"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

func wire(tag byte, parts ...[]byte) []byte {
	v := bytes.Join(parts, nil)
	out := []byte{tag}
	switch {
	case len(v) < 128:
		out = append(out, byte(len(v)))
	case len(v) < 256:
		out = append(out, 0x81, byte(len(v)))
	default:
		out = append(out, 0x82, byte(len(v)>>8), byte(len(v)))
	}
	return append(out, v...)
}
func envelope(id byte, op []byte, extra ...[]byte) []byte {
	p := [][]byte{wire(2, []byte{id}), op}
	return wire(0x30, append(p, extra...)...)
}
func bindReply(id byte) []byte {
	return envelope(id, wire(0x61, wire(0x0a, []byte{0}), wire(4), wire(4)))
}
func testBounds() bounds { return bounds{64 << 10, 1 << 20, 16, 1024} }

func TestBERFrameBoundaries(t *testing.T) {
	b := testBounds()
	valid := bindReply(1)
	if e := inspectFrame(valid, b); e != nil {
		t.Fatal(e)
	}
	deep := wire(4)
	for i := 0; i < 20; i++ {
		deep = wire(0x30, deep)
	}
	many := make([][]byte, 1100)
	for i := range many {
		many[i] = wire(4)
	}
	paging := func(value []byte) []byte {
		return wire(0xa0, wire(0x30, wire(4, []byte("1.2.840.113556.1.4.319")), wire(4, value)))
	}
	cases := map[string][]byte{
		"indefinite": {0x30, 0x80, 0, 0}, "long-length": {0x30, 0x85, 1, 0, 0, 0, 0}, "truncated": valid[:len(valid)-1],
		"nonminimal": {0x30, 0x81, 1, 0}, "high-tag": {0x3f, 1, 0}, "unexpected-operation": envelope(1, wire(0x78)),
		"deep": envelope(1, wire(0x64, deep)), "many-nodes": envelope(1, wire(0x64, many...)),
		"unknown-control":   envelope(1, wire(0x65), wire(0xa0, wire(0x30, wire(4, []byte("1.2.3")), wire(4, deep)))),
		"deep-paging-value": envelope(1, wire(0x65), paging(deep)),
		"paging-trailing":   envelope(1, wire(0x65), paging(append(wire(0x30, wire(2, []byte{0}), wire(4)), 0))),
	}
	for name, v := range cases {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(inspectFrame(v, b), errFrame) {
				t.Fatal("invalid frame accepted")
			}
		})
	}
	good := envelope(1, wire(0x65, wire(0x0a, []byte{0}), wire(4), wire(4)), paging(wire(0x30, wire(2, []byte{0}), wire(4, []byte("opaque")))))
	if e := inspectFrame(good, b); e != nil {
		t.Fatal(e)
	}
}

type bytesConn struct{ *bytes.Reader }

func (c bytesConn) Write(p []byte) (int, error)      { return 0, io.ErrClosedPipe }
func (c bytesConn) Close() error                     { return nil }
func (c bytesConn) LocalAddr() net.Addr              { return nil }
func (c bytesConn) RemoteAddr() net.Addr             { return nil }
func (c bytesConn) SetDeadline(time.Time) error      { return nil }
func (c bytesConn) SetReadDeadline(time.Time) error  { return nil }
func (c bytesConn) SetWriteDeadline(time.Time) error { return nil }
func TestFrameBudgetBeforeAllocation(t *testing.T) {
	b := testBounds()
	g := &frameConn{Conn: bytesConn{bytes.NewReader([]byte{0x30, 0x84, 0x7f, 0xff, 0xff, 0xff})}, limits: b}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, e := g.Read(make([]byte, 1)); !errors.Is(e, errFrame) {
		t.Fatal(e)
	}
	runtime.ReadMemStats(&after)
	if after.TotalAlloc-before.TotalAlloc > 128<<10 {
		t.Fatal("oversized declaration allocated payload")
	}
	v := bindReply(1)
	b.total = len(v)*2 - 1
	g = &frameConn{Conn: bytesConn{bytes.NewReader(append(v, v...))}, limits: b}
	out := make([]byte, len(v))
	if _, e := io.ReadFull(g, out); e != nil || !bytes.Equal(v, out) {
		t.Fatal(e)
	}
	if _, e := g.Read(out); !errors.Is(e, errFrame) {
		t.Fatal("connection budget not enforced", e)
	}
}
