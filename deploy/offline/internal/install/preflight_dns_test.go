package install

import (
	"bytes"
	"net"
	"testing"
)

func TestPreflightUsesTheSelectedDNSAddress(t *testing.T) {
	c := defaults()
	c.Origins = OriginsConfig{
		Admin:  "https://resolver-only.lane-h.test:8443",
		Portal: "https://127.0.0.1:8443",
	}
	if err := checkOriginResolution(t.Context(), c, servePreflightDNS(t)); err != nil {
		t.Fatal(err)
	}
}

// servePreflightDNS supplies one A record without forwarding or using any host DNS configuration.
func servePreflightDNS(t *testing.T) string {
	t.Helper()
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		server.Close()
		<-done
	})
	go func() {
		defer close(done)
		name := []byte("\x0dresolver-only\x06lane-h\x04test\x00")
		const headerSize, queryTypeAndClass = 12, 4
		end := headerSize + len(name) + queryTypeAndClass
		query := make([]byte, 512)
		for {
			size, source, err := server.ReadFrom(query)
			if err != nil {
				return
			}
			if size < end || !bytes.Equal(query[headerSize:end-queryTypeAndClass], name) {
				continue
			}
			// Return the exact question, with one address for A and no data for AAAA.
			response := append([]byte(nil), query[:end]...)
			copy(response[2:headerSize], []byte{0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0})
			if bytes.Equal(query[end-4:end], []byte{0, 1, 0, 1}) {
				response[7] = 1
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 1)
			}
			if _, err := server.WriteTo(response, source); err != nil {
				return
			}
		}
	}()
	return server.LocalAddr().String()
}
