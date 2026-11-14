// Package egress records outbound connection attempts in the zero-egress test harness.
//
// The end-to-end runner starts its Docker-in-Docker host with no network interface, then
// gives it one TUN device as the default route. Nothing is behind that device: every
// packet the host or its containers try to send off the machine arrives at the recorder,
// which writes it down as an Attempt and answers with an ICMP "administratively
// prohibited" error so the sender fails at once, as it would on an air-gapped network
// that rejects traffic.
package egress

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// IP protocol numbers the recorder decodes.
const (
	protocolICMP   = 1
	protocolTCP    = 6
	protocolUDP    = 17
	protocolICMPv6 = 58
)

// Header sizes and offsets from RFC 791, RFC 8200, RFC 768, RFC 792 and RFC 4443.
const (
	ipv4MinHeader          = 20
	ipv6Header             = 40
	udpHeader              = 8
	icmpHeader             = 8
	dnsHeader              = 12
	dnsPort                = 53
	defaultTTL             = 64
	ipv6MinimumMTU         = 1280
	ipv4FragmentOffsetMask = 0x1fff
)

// ICMP types and codes for "communication administratively prohibited".
const (
	icmpv4Unreachable     = 3
	icmpv4AdminProhibited = 13
	icmpv6Unreachable     = 1
	icmpv6AdminProhibited = 1
	// RFC 4443 section 2.1: ICMPv6 types below 128 are errors.
	icmpv6FirstInformational = 128
)

// icmpv4Errors are the ICMPv4 error types of RFC 792 and RFC 1122: destination
// unreachable, source quench, redirect, time exceeded and parameter problem.
var icmpv4Errors = map[byte]bool{3: true, 4: true, 5: true, 11: true, 12: true}

// Attempt is one packet that tried to leave the host.
type Attempt struct {
	Time        time.Time      `json:"time"`
	Protocol    string         `json:"protocol"`
	Source      netip.AddrPort `json:"source"`
	Destination netip.AddrPort `json:"destination"`
	// DNSQuestion is the queried name and type when the packet is a DNS query.
	DNSQuestion string `json:"dns_question,omitempty"`
}

// String formats the attempt for test failure messages.
func (a Attempt) String() string {
	text := fmt.Sprintf("%s %s -> %s", a.Protocol, a.Source, a.Destination)
	if a.DNSQuestion != "" {
		text += " dns " + a.DNSQuestion
	}
	return text
}

// packet is a decoded IP packet.
type packet struct {
	raw         []byte
	version     int
	protocol    uint8
	source      netip.Addr
	destination netip.Addr
	payload     []byte
}

var errMalformed = errors.New("malformed packet")

func decode(raw []byte) (packet, error) {
	if len(raw) == 0 {
		return packet{}, errMalformed
	}
	p := packet{raw: raw, version: int(raw[0] >> 4)}
	switch p.version {
	case 4:
		headerLength := int(raw[0]&0x0f) * 4
		if headerLength < ipv4MinHeader || len(raw) < headerLength {
			return packet{}, errMalformed
		}
		total := int(binary.BigEndian.Uint16(raw[2:4]))
		if total < headerLength || total > len(raw) {
			return packet{}, errMalformed
		}
		p.protocol = raw[9]
		p.source = netip.AddrFrom4([4]byte(raw[12:16]))
		p.destination = netip.AddrFrom4([4]byte(raw[16:20]))
		// Only the first fragment carries the transport header.
		if binary.BigEndian.Uint16(raw[6:8])&ipv4FragmentOffsetMask == 0 {
			p.payload = raw[headerLength:total]
		}
	case 6:
		if len(raw) < ipv6Header {
			return packet{}, errMalformed
		}
		total := ipv6Header + int(binary.BigEndian.Uint16(raw[4:6]))
		if total > len(raw) {
			return packet{}, errMalformed
		}
		// Extension headers are not followed: the kernel sends none on a plain connect.
		p.protocol = raw[6]
		p.source = netip.AddrFrom16([16]byte(raw[8:24]))
		p.destination = netip.AddrFrom16([16]byte(raw[24:40]))
		p.payload = raw[ipv6Header:total]
	default:
		return packet{}, errMalformed
	}
	return p, nil
}

// leavesLink reports whether the destination is beyond the local link. Link-local
// traffic, such as IPv6 router solicitations the kernel sends on any new interface,
// cannot leave the host and is not an attempt by the software under test.
func (p packet) leavesLink() bool {
	d := p.destination
	return !(d.IsLinkLocalMulticast() || d.IsLinkLocalUnicast() || d.IsInterfaceLocalMulticast())
}

// isICMPError reports whether the packet is itself an ICMP error, which must never be
// answered with another one.
func (p packet) isICMPError() bool {
	if len(p.payload) == 0 {
		return false
	}
	kind := p.payload[0]
	switch p.protocol {
	case protocolICMP:
		return icmpv4Errors[kind]
	case protocolICMPv6:
		return kind < icmpv6FirstInformational
	}
	return false
}

func (p packet) attempt(now time.Time) Attempt {
	a := Attempt{Time: now.UTC(), Protocol: protocolName(p.protocol)}
	var sourcePort, destinationPort uint16
	if (p.protocol == protocolTCP || p.protocol == protocolUDP) && len(p.payload) >= 4 {
		sourcePort = binary.BigEndian.Uint16(p.payload[0:2])
		destinationPort = binary.BigEndian.Uint16(p.payload[2:4])
	}
	a.Source = netip.AddrPortFrom(p.source, sourcePort)
	a.Destination = netip.AddrPortFrom(p.destination, destinationPort)
	if p.protocol == protocolUDP && destinationPort == dnsPort && len(p.payload) > udpHeader {
		a.DNSQuestion = dnsQuestion(p.payload[udpHeader:])
	}
	return a
}

func protocolName(protocol uint8) string {
	switch protocol {
	case protocolICMP:
		return "icmp"
	case protocolTCP:
		return "tcp"
	case protocolUDP:
		return "udp"
	case protocolICMPv6:
		return "icmpv6"
	}
	return fmt.Sprintf("ip-%d", protocol)
}

// dnsTypes names the query types worth showing; others print as numbers.
var dnsTypes = map[uint16]string{1: "A", 5: "CNAME", 12: "PTR", 15: "MX", 16: "TXT", 28: "AAAA", 33: "SRV", 65: "HTTPS"}

// dnsQuestion returns "name TYPE" for the first question of a DNS message, or "" when
// the message does not parse. Compression pointers do not occur in a query's question.
func dnsQuestion(message []byte) string {
	if len(message) < dnsHeader || binary.BigEndian.Uint16(message[4:6]) == 0 {
		return ""
	}
	var labels []string
	offset := dnsHeader
	for {
		if offset >= len(message) {
			return ""
		}
		length := int(message[offset])
		offset++
		if length == 0 {
			break
		}
		if length > 63 || offset+length > len(message) {
			return ""
		}
		labels = append(labels, string(message[offset:offset+length]))
		offset += length
	}
	if offset+2 > len(message) {
		return ""
	}
	kind := binary.BigEndian.Uint16(message[offset : offset+2])
	name, ok := dnsTypes[kind]
	if !ok {
		name = fmt.Sprintf("TYPE%d", kind)
	}
	return strings.Join(labels, ".") + ". " + name
}

// rejection builds the ICMP "administratively prohibited" error that answers p, or nil
// when p must not be answered.
func rejection(p packet) []byte {
	if p.isICMPError() || p.destination.IsMulticast() || p.source.IsUnspecified() {
		return nil
	}
	if p.version == 4 {
		// RFC 792: the original IP header and the first 8 bytes of its payload.
		quoted := p.raw[:min(len(p.raw), int(p.raw[0]&0x0f)*4+8)]
		reply := make([]byte, ipv4MinHeader+icmpHeader+len(quoted))
		reply[0] = 0x45
		binary.BigEndian.PutUint16(reply[2:4], uint16(len(reply)))
		reply[8] = defaultTTL
		reply[9] = protocolICMP
		copy(reply[12:16], p.destination.AsSlice())
		copy(reply[16:20], p.source.AsSlice())
		binary.BigEndian.PutUint16(reply[10:12], checksum(reply[:ipv4MinHeader], 0))
		message := reply[ipv4MinHeader:]
		message[0], message[1] = icmpv4Unreachable, icmpv4AdminProhibited
		copy(message[icmpHeader:], quoted)
		binary.BigEndian.PutUint16(message[2:4], checksum(message, 0))
		return reply
	}
	// RFC 4443: as much of the original as fits in the minimum MTU.
	quoted := p.raw[:min(len(p.raw), ipv6MinimumMTU-ipv6Header-icmpHeader)]
	reply := make([]byte, ipv6Header+icmpHeader+len(quoted))
	reply[0] = 0x60
	binary.BigEndian.PutUint16(reply[4:6], uint16(icmpHeader+len(quoted)))
	reply[6] = protocolICMPv6
	reply[7] = defaultTTL
	copy(reply[8:24], p.destination.AsSlice())
	copy(reply[24:40], p.source.AsSlice())
	message := reply[ipv6Header:]
	message[0], message[1] = icmpv6Unreachable, icmpv6AdminProhibited
	copy(message[icmpHeader:], quoted)
	binary.BigEndian.PutUint16(message[2:4], checksum(message, pseudoHeaderSum(reply[8:24], reply[24:40], len(message), protocolICMPv6)))
	return reply
}

// pseudoHeaderSum is the unfolded one's-complement sum of the IPv6 pseudo-header.
func pseudoHeaderSum(source, destination []byte, length int, protocol uint8) uint32 {
	var sum uint32
	for _, address := range [][]byte{source, destination} {
		for i := 0; i < len(address); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(address[i:]))
		}
	}
	return sum + uint32(length>>16) + uint32(length&0xffff) + uint32(protocol)
}

// checksum is the Internet checksum of RFC 1071 over data, starting from initial.
func checksum(data []byte, initial uint32) uint16 {
	sum := initial
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i:]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum > 0xffff {
		sum = sum>>16 + sum&0xffff
	}
	return ^uint16(sum)
}
