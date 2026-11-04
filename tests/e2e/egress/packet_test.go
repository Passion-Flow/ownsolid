package egress

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ipv4 builds an IPv4 packet with a valid header checksum.
func ipv4(t *testing.T, protocol uint8, source, destination string, payload []byte) []byte {
	t.Helper()
	raw := make([]byte, ipv4MinHeader+len(payload))
	raw[0] = 0x45
	binary.BigEndian.PutUint16(raw[2:4], uint16(len(raw)))
	raw[8] = defaultTTL
	raw[9] = protocol
	copy(raw[12:16], netip.MustParseAddr(source).AsSlice())
	copy(raw[16:20], netip.MustParseAddr(destination).AsSlice())
	binary.BigEndian.PutUint16(raw[10:12], checksum(raw[:ipv4MinHeader], 0))
	copy(raw[ipv4MinHeader:], payload)
	return raw
}

func ipv6(t *testing.T, protocol uint8, source, destination string, payload []byte) []byte {
	t.Helper()
	raw := make([]byte, ipv6Header+len(payload))
	raw[0] = 0x60
	binary.BigEndian.PutUint16(raw[4:6], uint16(len(payload)))
	raw[6] = protocol
	raw[7] = defaultTTL
	copy(raw[8:24], netip.MustParseAddr(source).AsSlice())
	copy(raw[24:40], netip.MustParseAddr(destination).AsSlice())
	copy(raw[ipv6Header:], payload)
	return raw
}

// ports builds a transport header prefix with the two ports and room for the rest.
func ports(source, destination uint16, rest []byte) []byte {
	header := make([]byte, 20)
	binary.BigEndian.PutUint16(header[0:2], source)
	binary.BigEndian.PutUint16(header[2:4], destination)
	return append(header, rest...)
}

// dnsQuery builds a UDP datagram carrying a one-question DNS query.
func dnsQuery(name string, kind uint16) []byte {
	message := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(name, ".") {
		message = append(message, byte(len(label)))
		message = append(message, label...)
	}
	message = append(message, 0, byte(kind>>8), byte(kind), 0, 1)
	datagram := make([]byte, udpHeader, udpHeader+len(message))
	binary.BigEndian.PutUint16(datagram[0:2], 40000)
	binary.BigEndian.PutUint16(datagram[2:4], dnsPort)
	binary.BigEndian.PutUint16(datagram[4:6], uint16(udpHeader+len(message)))
	return append(datagram, message...)
}

func TestAttemptDecodesTransportAndDNS(t *testing.T) {
	now := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name, want string
		raw        []byte
	}{
		{"ipv4 tcp", "tcp 198.18.0.1:51000 -> 1.1.1.1:443", ipv4(t, protocolTCP, "198.18.0.1", "1.1.1.1", ports(51000, 443, nil))},
		{"ipv6 tcp", "tcp [2001:db8::1]:51000 -> [2606:4700::1111]:443", ipv6(t, protocolTCP, "2001:db8::1", "2606:4700::1111", ports(51000, 443, nil))},
		{"dns query", "udp 198.18.0.1:40000 -> 10.1.1.10:53 dns registry-1.docker.io. AAAA", ipv4(t, protocolUDP, "198.18.0.1", "10.1.1.10", dnsQuery("registry-1.docker.io", 28))},
		{"icmp echo", "icmp 198.18.0.1:0 -> 8.8.8.8:0", ipv4(t, protocolICMP, "198.18.0.1", "8.8.8.8", []byte{8, 0, 0, 0, 0, 1, 0, 1})},
		{"other protocol", "ip-47 198.18.0.1:0 -> 203.0.113.9:0", ipv4(t, 47, "198.18.0.1", "203.0.113.9", []byte{0, 0, 0, 0})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := decode(c.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.attempt(now).String(); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestDecodeRejectsMalformedPackets(t *testing.T) {
	valid := ipv4(t, protocolTCP, "198.18.0.1", "1.1.1.1", ports(1, 2, nil))
	truncatedLength := bytes.Clone(valid)
	binary.BigEndian.PutUint16(truncatedLength[2:4], uint16(len(valid)+1))
	shortHeader := bytes.Clone(valid)
	shortHeader[0] = 0x44
	for name, raw := range map[string][]byte{
		"empty": nil, "unknown version": {0x70, 0, 0, 0}, "short ipv4": valid[:10],
		"length beyond data": truncatedLength, "header under minimum": shortHeader,
		"short ipv6": make([]byte, 39),
	} {
		if _, err := decode(raw); !errors.Is(err, errMalformed) {
			t.Errorf("%s: err = %v, want malformed", name, err)
		}
	}
}

func TestDNSQuestionToleratesBrokenMessages(t *testing.T) {
	query := dnsQuery("example.test", 1)[udpHeader:]
	for name, message := range map[string][]byte{
		"short header":      query[:5],
		"no questions":      append([]byte{0, 0, 0, 0, 0, 0}, query[6:]...),
		"label past end":    query[:len(query)-8],
		"missing type":      query[:len(query)-4],
		"oversized label":   append(bytes.Clone(query[:dnsHeader]), 64),
		"unterminated tail": query[:dnsHeader+1],
	} {
		if got := dnsQuestion(message); got != "" {
			t.Errorf("%s: got %q", name, got)
		}
	}
	if got := dnsQuestion(dnsQuery("example.test", 99)[udpHeader:]); got != "example.test. TYPE99" {
		t.Fatalf("unknown type: %q", got)
	}
}

func TestLinkLocalTrafficIsNotAnAttempt(t *testing.T) {
	for _, destination := range []string{"ff02::2", "ff02::16", "fe80::1", "224.0.0.22", "ff01::1"} {
		source := "fe80::1"
		var raw []byte
		if netip.MustParseAddr(destination).Is4() {
			raw = ipv4(t, 2, "198.18.0.1", destination, []byte{0x22, 0, 0, 0})
		} else {
			raw = ipv6(t, protocolICMPv6, source, destination, []byte{133, 0, 0, 0})
		}
		p, err := decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		if p.leavesLink() {
			t.Errorf("%s counted as leaving the link", destination)
		}
	}
}

func TestRejectionIsAValidAdministrativelyProhibitedError(t *testing.T) {
	t.Run("ipv4", func(t *testing.T) {
		original := ipv4(t, protocolTCP, "198.18.0.1", "1.1.1.1", ports(51000, 443, []byte("payload beyond eight bytes")))
		p, _ := decode(original)
		reply := rejection(p)
		if checksum(reply[:ipv4MinHeader], 0) != 0 {
			t.Fatal("IP header checksum does not verify")
		}
		message := reply[ipv4MinHeader:]
		if checksum(message, 0) != 0 || message[0] != icmpv4Unreachable || message[1] != icmpv4AdminProhibited {
			t.Fatalf("bad ICMP message % x", message[:4])
		}
		if !bytes.Equal(reply[12:16], original[16:20]) || !bytes.Equal(reply[16:20], original[12:16]) {
			t.Fatal("addresses are not swapped")
		}
		if quoted := message[icmpHeader:]; !bytes.Equal(quoted, original[:ipv4MinHeader+8]) {
			t.Fatalf("quotes %d bytes, want header plus 8", len(quoted))
		}
		if int(binary.BigEndian.Uint16(reply[2:4])) != len(reply) {
			t.Fatal("total length does not match")
		}
	})
	t.Run("ipv6", func(t *testing.T) {
		original := ipv6(t, protocolTCP, "2001:db8::1", "2606:4700::1111", ports(51000, 443, make([]byte, 1500)))
		p, _ := decode(original)
		reply := rejection(p)
		if len(reply) != ipv6MinimumMTU {
			t.Fatalf("reply is %d bytes, want the minimum MTU", len(reply))
		}
		message := reply[ipv6Header:]
		if checksum(message, pseudoHeaderSum(reply[8:24], reply[24:40], len(message), protocolICMPv6)) != 0 {
			t.Fatal("ICMPv6 checksum does not verify")
		}
		if message[0] != icmpv6Unreachable || message[1] != icmpv6AdminProhibited {
			t.Fatalf("bad ICMPv6 type % x", message[:2])
		}
	})
	t.Run("never answers errors, multicast or unspecified sources", func(t *testing.T) {
		for name, raw := range map[string][]byte{
			"icmp error":   ipv4(t, protocolICMP, "198.18.0.1", "1.1.1.1", []byte{3, 1, 0, 0, 0, 0, 0, 0}),
			"icmpv6 error": ipv6(t, protocolICMPv6, "2001:db8::1", "2001:db8::2", []byte{1, 4, 0, 0, 0, 0, 0, 0}),
			"multicast":    ipv4(t, protocolUDP, "198.18.0.1", "239.1.1.1", ports(1, 2, nil)),
			"unspecified":  ipv4(t, protocolUDP, "0.0.0.0", "1.1.1.1", ports(68, 67, nil)),
		} {
			p, err := decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if rejection(p) != nil {
				t.Errorf("%s was answered", name)
			}
		}
		echo, _ := decode(ipv4(t, protocolICMP, "198.18.0.1", "8.8.8.8", []byte{8, 0, 0, 0, 0, 1, 0, 1}))
		if rejection(echo) == nil {
			t.Error("an echo request was not rejected")
		}
	})
}

// device is an in-memory TUN device: reads return queued packets, writes are captured.
type device struct {
	in      [][]byte
	written [][]byte
}

func (d *device) Read(p []byte) (int, error) {
	if len(d.in) == 0 {
		return 0, os.ErrClosed
	}
	n := copy(p, d.in[0])
	d.in = d.in[1:]
	return n, nil
}

func (d *device) Write(p []byte) (int, error) {
	d.written = append(d.written, bytes.Clone(p))
	return len(p), nil
}

func TestRecorderLogsRejectsAndReadsBack(t *testing.T) {
	d := &device{in: [][]byte{
		ipv4(t, protocolTCP, "198.18.0.1", "192.0.2.1", ports(51000, 9, nil)),
		{0xff, 0x00},
		ipv6(t, protocolICMPv6, "fe80::1", "ff02::2", []byte{133, 0, 0, 0}),
		ipv4(t, protocolUDP, "198.18.0.1", "10.1.1.10", dnsQuery("example.test", 1)),
	}}
	path := filepath.Join(t.TempDir(), "egress.jsonl")
	log, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRecorder(d, log)
	r.now = func() time.Time { return time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC) }
	if err = r.Run(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("run ended with %v", err)
	}
	log.Close()
	if len(d.written) != 2 {
		t.Fatalf("%d rejections, want 2", len(d.written))
	}
	attempts, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].String() != "tcp 198.18.0.1:51000 -> 192.0.2.1:9" || attempts[1].DNSQuestion != "example.test. A" {
		t.Fatalf("attempts: %v", attempts)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrShortWrite }

func TestRecorderStopsWhenTheLogFails(t *testing.T) {
	d := &device{in: [][]byte{ipv4(t, protocolTCP, "198.18.0.1", "1.1.1.1", ports(1, 443, nil))}}
	if err := NewRecorder(d, failingWriter{}).Run(); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got %v", err)
	}
}

func TestReadLog(t *testing.T) {
	directory := t.TempDir()
	if _, err := ReadLog(filepath.Join(directory, "absent")); err == nil {
		t.Fatal("a missing log was accepted")
	}
	broken := filepath.Join(directory, "broken.jsonl")
	if err := os.WriteFile(broken, []byte("{\"protocol\":\"tcp\"}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLog(broken); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("got %v", err)
	}
	empty := filepath.Join(directory, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if attempts, err := ReadLog(empty); err != nil || len(attempts) != 0 {
		t.Fatalf("%v %v", attempts, err)
	}
}
