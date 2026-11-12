package main

import (
	"encoding/binary"
	"net"
	"os"
	"time"
)

func main() {
	for _, a := range []struct{ network, address string }{{"tcp4", "192.0.2.10:9"}, {"tcp6", "[2001:db8::10]:9"}} {
		c, e := net.DialTimeout(a.network, a.address, 500*time.Millisecond)
		if e == nil {
			c.Close()
			os.Exit(1)
		}
	}
	c, e := net.Dial("udp4", "192.0.2.53:53")
	if e != nil {
		panic(e)
	}
	packet := make([]byte, 12)
	binary.BigEndian.PutUint16(packet, 1)
	binary.BigEndian.PutUint16(packet[2:], 256)
	binary.BigEndian.PutUint16(packet[4:], 1)
	packet = append(packet, []byte("\x05probe\x07invalid\x00\x00\x01\x00\x01")...)
	if _, e = c.Write(packet); e != nil {
		panic(e)
	}
	c.Close()
}
