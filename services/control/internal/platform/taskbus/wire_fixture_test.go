//go:build repository_integration && task_events_integration && task_events_wire_integration

package taskbus

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestTaskBusWireFixtureFrameBoundsAndExactForwarding(t *testing.T) {
	for _, raw := range []string{"PUB event reply 4\r\nbody\r\n", "MSG reply 3 0\r\n\r\n", "HPUB event reply 12 16\r\nNATS/1.0\r\n\r\nbody\r\n", "HMSG reply 3 ack 12 16\r\nNATS/1.0\r\n\r\nbody\r\n", "INFO {\"nonce\":\"synthetic-only\"}\r\n"} {
		f, e := readWireFrame(bufio.NewReader(strings.NewReader(raw)))
		if e != nil {
			t.Fatal(e)
		}
		var out bytes.Buffer
		if e = writeWireFrame(&out, f.raw); e != nil || out.String() != raw {
			t.Fatal("wire fixture rewrote bytes", e)
		}
	}
	for _, raw := range []string{"PUB x 1048577\r\n", "MSG x 3 -1\r\n", "HPUB x 20 4\r\nbody\r\n", "MSG x 3 4\r\nbad", "PUB x 4\nbody\n", "UNKNOWN x\r\n"} {
		if _, e := readWireFrame(bufio.NewReader(strings.NewReader(raw))); e == nil {
			t.Fatal("invalid/bounded frame accepted")
		}
	}
}
