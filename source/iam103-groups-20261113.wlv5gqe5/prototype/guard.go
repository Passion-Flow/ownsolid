package directory

import (
	"bytes"
	"errors"
	"io"
	"net"
)

var errFrame = errors.New("DIRECTORY_FRAME_REFUSED")

type bounds struct{ frame, total, depth, nodes int }
type frameConn struct {
	net.Conn
	limits  bounds
	pending []byte
	used    int
}
type tlv struct {
	tag      byte
	value    []byte
	children []tlv
}

// The SDK parses control OCTET STRING values as another BER tree. Validate that
// embedded tree too, and refuse controls this adapter never requests.
func inspectFrame(b []byte, lim bounds) error {
	count := 0
	var parse func([]byte, int) (tlv, int, error)
	parse = func(b []byte, depth int) (tlv, int, error) {
		if depth > lim.depth || len(b) < 2 || b[0]&31 == 31 {
			return tlv{}, 0, errFrame
		}
		count++
		if count > lim.nodes {
			return tlv{}, 0, errFrame
		}
		n, h := int(b[1]), 2
		if n&128 != 0 {
			k := n & 127
			if k == 0 || k > 4 || len(b) < 2+k || b[2] == 0 {
				return tlv{}, 0, errFrame
			}
			n = 0
			for _, v := range b[2 : 2+k] {
				n = n*256 + int(v)
			}
			h += k
			if n < 128 {
				return tlv{}, 0, errFrame
			}
		}
		if n > lim.frame || n > len(b)-h {
			return tlv{}, 0, errFrame
		}
		v := tlv{tag: b[0], value: b[h : h+n]}
		if v.tag&32 != 0 {
			for off := 0; off < n; {
				c, k, e := parse(v.value[off:], depth+1)
				if e != nil {
					return tlv{}, 0, e
				}
				v.children = append(v.children, c)
				off += k
			}
		}
		return v, h + n, nil
	}
	root, n, e := parse(b, 1)
	if e != nil || n != len(b) || root.tag != 0x30 || len(root.children) < 2 || len(root.children) > 3 || root.children[0].tag != 2 {
		return errFrame
	}
	switch root.children[1].tag {
	case 0x61, 0x64, 0x65, 0x73:
	default:
		return errFrame
	}
	if len(root.children) == 3 {
		controls := root.children[2]
		if controls.tag != 0xa0 {
			return errFrame
		}
		if len(controls.children) > 1 {
			return errFrame
		}
		for _, c := range controls.children {
			if c.tag != 0x30 || (len(c.children) != 2 && len(c.children) != 3) || c.children[0].tag != 4 || c.children[len(c.children)-1].tag != 4 || !bytes.Equal(c.children[0].value, []byte("1.2.840.113556.1.4.319")) {
				return errFrame
			}
			if len(c.children) == 3 {
				v := c.children[1]
				if v.tag != 1 || len(v.value) != 1 || (v.value[0] != 0 && v.value[0] != 255) {
					return errFrame
				}
			}
			value := c.children[len(c.children)-1].value
			embedded, n, e := parse(value, 4)
			if e != nil || n != len(value) || embedded.tag != 0x30 || len(embedded.children) != 2 || embedded.children[0].tag != 2 || embedded.children[1].tag != 4 {
				return errFrame
			}
		}
	}
	return nil
}

func (g *frameConn) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if len(g.pending) == 0 {
		var header [6]byte
		if _, e := io.ReadFull(g.Conn, header[:2]); e != nil {
			return 0, e
		}
		if header[0] != 0x30 {
			return 0, errFrame
		}
		h, n := 2, int(header[1])
		if n&128 != 0 {
			k := n & 127
			if k == 0 || k > 4 {
				return 0, errFrame
			}
			if _, e := io.ReadFull(g.Conn, header[2:2+k]); e != nil {
				return 0, e
			}
			if header[2] == 0 {
				return 0, errFrame
			}
			n = 0
			for _, v := range header[2 : 2+k] {
				n = n*256 + int(v)
			}
			h += k
			if n < 128 {
				return 0, errFrame
			}
		}
		if n > g.limits.frame-h || h+n > g.limits.total-g.used {
			return 0, errFrame
		}
		frame := make([]byte, h+n)
		copy(frame, header[:h])
		if _, e := io.ReadFull(g.Conn, frame[h:]); e != nil {
			return 0, e
		}
		if e := inspectFrame(frame, g.limits); e != nil {
			return 0, e
		}
		g.used += len(frame)
		g.pending = frame
	}
	n := copy(dst, g.pending)
	g.pending = g.pending[n:]
	return n, nil
}
