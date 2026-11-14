//go:build repository_integration

// Package commitfault owns test-only loopback faults against synthetic databases.
// It forwards real protocol/TLS traffic and never supplies database results.
package commitfault

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type Mode uint32

const (
	Pass Mode = iota
	DropBeforeCommit
	DropCommitAcknowledgement
)

const mysqlQueryCommand = 0x03
const mysqlSSLFlag = 1 << 11
const mysqlCompressionFlag = 1 << 5
const mysqlQueryAttributesFlag = 1 << 27
const mysqlSSLRequestBytes = 32
const maximumClassicPacketBytes = (1 << 24) - 1
const maximumProxyConnections = 2
const proxyConnectionSeconds = 10

type Observation struct {
	Mode       Mode
	Forwarded  bool
	ServerOK   bool
	Suppressed bool
}

type MySQL struct {
	listener net.Listener
	upstream string
	tls      *tls.Config
	server   tls.Certificate
	mode     atomic.Uint32
	commits  atomic.Int32
	outcomes chan Observation
	slots    chan struct{}
	mu       sync.Mutex
	closed   bool
	conns    map[net.Conn]struct{}
	wg       sync.WaitGroup
}

func privateFixtureFile(path string) ([]byte, error) {
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path || !filepath.IsAbs(path) {
		return nil, errors.New("COMMIT_FAULT_PRIVATE_INPUT_REQUIRED")
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > 1<<20 {
		return nil, errors.New("COMMIT_FAULT_PRIVATE_INPUT_REQUIRED")
	}
	return os.ReadFile(path)
}

func NewMySQL(c mysqlstore.Configuration) (*MySQL, error) {
	if c.Host != "127.0.0.1" || c.ServerName != "localhost" || c.Validate() != nil {
		return nil, errors.New("COMMIT_FAULT_EXCLUSIVE_LOOPBACK_FIXTURE_REQUIRED")
	}
	ca, e := privateFixtureFile(c.CAFile)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("COMMIT_FAULT_CA_INVALID")
	}
	directory := filepath.Dir(c.PasswordFile)
	certificate, e := privateFixtureFile(filepath.Join(directory, "server.pem"))
	if e != nil {
		return nil, e
	}
	key, e := privateFixtureFile(filepath.Join(directory, "server-key.pem"))
	if e != nil {
		return nil, e
	}
	server, e := tls.X509KeyPair(certificate, key)
	if e != nil {
		return nil, errors.New("COMMIT_FAULT_CERTIFICATE_INVALID")
	}
	listener, e := net.Listen("tcp4", net.JoinHostPort(c.Host, "0"))
	if e != nil {
		return nil, errors.New("COMMIT_FAULT_LOOPBACK_LISTENER_UNAVAILABLE")
	}
	p := &MySQL{listener: listener, upstream: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), tls: &tls.Config{RootCAs: roots, ServerName: c.ServerName, MinVersion: tls.VersionTLS12}, server: server, outcomes: make(chan Observation, 1), slots: make(chan struct{}, maximumProxyConnections), conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.accept()
	return p, nil
}

func (p *MySQL) Port() int                    { return p.listener.Addr().(*net.TCPAddr).Port }
func (p *MySQL) Commits() int32               { return p.commits.Load() }
func (p *MySQL) Outcomes() <-chan Observation { return p.outcomes }
func (p *MySQL) Arm(mode Mode) error {
	if mode != DropBeforeCommit && mode != DropCommitAcknowledgement || !p.mode.CompareAndSwap(uint32(Pass), uint32(mode)) {
		return errors.New("COMMIT_FAULT_MODE_INVALID_OR_BUSY")
	}
	return nil
}
func (p *MySQL) remember(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}
func (p *MySQL) forget(conn net.Conn) {
	conn.Close()
	p.mu.Lock()
	delete(p.conns, conn)
	p.mu.Unlock()
}
func (p *MySQL) accept() {
	defer p.wg.Done()
	for {
		conn, e := p.listener.Accept()
		if e != nil {
			return
		}
		select {
		case p.slots <- struct{}{}:
			if !p.remember(conn) {
				<-p.slots
				return
			}
			p.wg.Add(1)
			go func() { defer p.wg.Done(); defer func() { <-p.slots }(); defer p.forget(conn); p.forward(conn) }()
		default:
			conn.Close()
		}
	}
}

func classicPacket(reader io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, e := io.ReadFull(reader, header); e != nil {
		return nil, e
	}
	size := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	if size == 0 || size >= maximumClassicPacketBytes {
		return nil, errors.New("COMMIT_FAULT_UNSUPPORTED_PACKET")
	}
	packet := make([]byte, 4+size)
	copy(packet, header)
	_, e := io.ReadFull(reader, packet[4:])
	return packet, e
}
func writePacket(writer io.Writer, packet []byte) error {
	for len(packet) > 0 {
		n, e := writer.Write(packet)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nil
}
func (p *MySQL) observe(value Observation) {
	select {
	case p.outcomes <- value:
	default:
	}
}
func (p *MySQL) forward(client net.Conn) {
	upstream, e := net.DialTimeout("tcp4", p.upstream, proxyConnectionSeconds*time.Second)
	if e != nil {
		return
	}
	if !p.remember(upstream) {
		return
	}
	defer p.forget(upstream)
	deadline := time.Now().Add(proxyConnectionSeconds * time.Second)
	client.SetDeadline(deadline)
	upstream.SetDeadline(deadline)
	greeting, e := classicPacket(upstream)
	if e != nil || greeting[3] != 0 || writePacket(client, greeting) != nil {
		return
	}
	request, e := classicPacket(client)
	if e != nil || len(request) != 4+mysqlSSLRequestBytes || request[3] != 1 {
		return
	}
	flags := binary.LittleEndian.Uint32(request[4:8])
	if flags&mysqlSSLFlag == 0 || flags&(mysqlCompressionFlag|mysqlQueryAttributesFlag) != 0 || writePacket(upstream, request) != nil {
		return
	}
	// Both real TLS sessions verify the configured CA/hostname at the client
	// boundary. Credentials pass only through these test-owned encrypted links.
	backend := tls.Client(upstream, p.tls.Clone())
	frontend := tls.Server(client, &tls.Config{Certificates: []tls.Certificate{p.server}, MinVersion: tls.VersionTLS12})
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if backend.HandshakeContext(ctx) != nil || frontend.HandshakeContext(ctx) != nil {
		return
	}
	var awaiting atomic.Uint32
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		for {
			packet, e := classicPacket(backend)
			if e != nil {
				return
			}
			if awaiting.CompareAndSwap(uint32(DropCommitAcknowledgement), uint32(Pass)) {
				// The actual server's COMMIT OK packet is consumed, never sent to
				// the application. Independent domain readback must still prove it.
				p.observe(Observation{Mode: DropCommitAcknowledgement, Forwarded: true, ServerOK: packet[3] == 1 && len(packet) >= 11 && packet[4] == 0x00, Suppressed: true})
				return
			}
			if writePacket(frontend, packet) != nil {
				return
			}
		}
	}()
	for {
		packet, e := classicPacket(frontend)
		if e != nil {
			break
		}
		if packet[3] == 0 && bytes.Equal(packet[4:], append([]byte{mysqlQueryCommand}, []byte("COMMIT")...)) {
			p.commits.Add(1)
			mode := Mode(p.mode.Swap(uint32(Pass)))
			if mode == DropBeforeCommit {
				p.observe(Observation{Mode: mode, Suppressed: true})
				break
			}
			if mode == DropCommitAcknowledgement {
				awaiting.Store(uint32(mode))
			}
		}
		if writePacket(backend, packet) != nil {
			break
		}
	}
	upstream.Close()
	client.Close()
	<-done
}

func (p *MySQL) Close(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	p.listener.Close()
	for conn := range p.conns {
		conn.Close()
	}
	p.mu.Unlock()
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("COMMIT_FAULT_SHUTDOWN_TIMEOUT")
	}
}
