//go:build repository_integration

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
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const postgresProtocolVersion3 = 196608
const maximumPostgresTestMessageBytes = 1 << 20

// PlaintextPostgreSQL retains its historical type name for existing test callers.
// NewPlaintextPostgreSQL refuses SSL/GSS negotiation; NewPostgreSQL preserves
// explicit verified TLS on both legs. Neither mode qualifies customer deployment.
type PlaintextPostgreSQL struct {
	listener    net.Listener
	upstream    string
	mode        atomic.Uint32
	commits     atomic.Int32
	outcomes    chan Observation
	mu          sync.Mutex
	closed      bool
	conns       map[net.Conn]struct{}
	wg          sync.WaitGroup
	slots       chan struct{}
	upstreamTLS *tls.Config
	frontendTLS *tls.Config
}

// NewPostgreSQL preserves the declared isolated transport. TLS mode authenticates
// both legs with the actual fixture certificate and original CA/hostname policy;
// it never changes sslmode, disables verification or supplies SQL responses.
func NewPostgreSQL(host string, port uint16, configuration *tls.Config, certificateFile, keyFile string) (*PlaintextPostgreSQL, error) {
	if configuration == nil {
		return NewPlaintextPostgreSQL(host, port)
	}
	if configuration.InsecureSkipVerify || configuration.RootCAs == nil || configuration.ServerName == "" {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_VERIFIED_TLS_REQUIRED")
	}
	certificate, e := tls.LoadX509KeyPair(certificateFile, keyFile)
	if e != nil || len(certificate.Certificate) == 0 {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_EXACT_FIXTURE_TLS_PAIR_REQUIRED")
	}
	leaf, e := x509.ParseCertificate(certificate.Certificate[0])
	if e != nil || leaf.VerifyHostname(configuration.ServerName) != nil {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_FIXTURE_TLS_NAME_REQUIRED")
	}
	upstream := configuration.Clone()
	if upstream.MinVersion < tls.VersionTLS12 {
		upstream.MinVersion = tls.VersionTLS12
	}
	return newPostgreSQL(host, port, upstream, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
}
func NewPlaintextPostgreSQL(host string, port uint16) (*PlaintextPostgreSQL, error) {
	return newPostgreSQL(host, port, nil, nil)
}
func newPostgreSQL(host string, port uint16, upstreamTLS, frontendTLS *tls.Config) (*PlaintextPostgreSQL, error) {
	if host != "127.0.0.1" || port < 1024 {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_EXCLUSIVE_PLAINTEXT_LOOPBACK_REQUIRED")
	}
	listener, e := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if e != nil {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_LISTENER_UNAVAILABLE")
	}
	p := &PlaintextPostgreSQL{upstreamTLS: upstreamTLS, frontendTLS: frontendTLS, listener: listener, upstream: net.JoinHostPort(host, strconv.Itoa(int(port))), outcomes: make(chan Observation, 1), conns: map[net.Conn]struct{}{}, slots: make(chan struct{}, maximumProxyConnections)}
	p.wg.Add(1)
	go p.accept()
	return p, nil
}
func (p *PlaintextPostgreSQL) Port() uint16                 { return uint16(p.listener.Addr().(*net.TCPAddr).Port) }
func (p *PlaintextPostgreSQL) Commits() int32               { return p.commits.Load() }
func (p *PlaintextPostgreSQL) Outcomes() <-chan Observation { return p.outcomes }
func (p *PlaintextPostgreSQL) Arm(mode Mode) error {
	if mode != DropBeforeCommit && mode != DropCommitAcknowledgement || !p.mode.CompareAndSwap(uint32(Pass), uint32(mode)) {
		return errors.New("POSTGRES_COMMIT_FAULT_MODE_INVALID_OR_BUSY")
	}
	return nil
}
func (p *PlaintextPostgreSQL) remember(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		conn.Close()
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}
func (p *PlaintextPostgreSQL) forget(conn net.Conn) {
	conn.Close()
	p.mu.Lock()
	delete(p.conns, conn)
	p.mu.Unlock()
}
func (p *PlaintextPostgreSQL) accept() {
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
func postgresStartup(reader io.Reader) ([]byte, error) {
	header := make([]byte, 8)
	if _, e := io.ReadFull(reader, header); e != nil {
		return nil, e
	}
	size := binary.BigEndian.Uint32(header[:4])
	if size < 8 || size > maximumPostgresTestMessageBytes || binary.BigEndian.Uint32(header[4:]) != postgresProtocolVersion3 {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_ONLY_EXPLICIT_PLAINTEXT_STARTUP_SUPPORTED")
	}
	packet := make([]byte, int(size))
	copy(packet, header)
	_, e := io.ReadFull(reader, packet[8:])
	return packet, e
}
func postgresPacket(reader io.Reader) ([]byte, error) {
	header := make([]byte, 5)
	if _, e := io.ReadFull(reader, header); e != nil {
		return nil, e
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size < 4 || size > maximumPostgresTestMessageBytes {
		return nil, errors.New("POSTGRES_COMMIT_FAULT_MESSAGE_BOUND_REQUIRED")
	}
	packet := make([]byte, int(size)+1)
	copy(packet, header)
	_, e := io.ReadFull(reader, packet[5:])
	return packet, e
}
func (p *PlaintextPostgreSQL) forward(client net.Conn) {
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
	if p.upstreamTLS != nil {
		// PostgreSQL SSLRequest is eight bytes and must receive S on both legs.
		request := []byte{0, 0, 0, 8, 4, 210, 22, 47}
		received := make([]byte, 8)
		if _, e = io.ReadFull(client, received); e != nil || !bytes.Equal(received, request) {
			return
		}
		if writePacket(upstream, request) != nil {
			return
		}
		response := make([]byte, 1)
		if _, e = io.ReadFull(upstream, response); e != nil || response[0] != 'S' {
			return
		}
		if writePacket(client, []byte{'S'}) != nil {
			return
		}
		frontend := tls.Server(client, p.frontendTLS.Clone())
		backend := tls.Client(upstream, p.upstreamTLS.Clone())
		if frontend.Handshake() != nil || backend.Handshake() != nil {
			return
		}
		client, upstream = frontend, backend
	}
	startup, e := postgresStartup(client)
	if e != nil || writePacket(upstream, startup) != nil {
		return
	}
	var awaiting atomic.Uint32
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer client.Close()
		for {
			packet, e := postgresPacket(upstream)
			if e != nil {
				return
			}
			if awaiting.Load() == uint32(DropCommitAcknowledgement) {
				// Discard asynchronous notices without treating them as a COMMIT
				// result. The real CommandComplete must name COMMIT before success.
				if packet[0] == 'N' || packet[0] == 'A' || packet[0] == 'S' {
					continue
				}
				if awaiting.CompareAndSwap(uint32(DropCommitAcknowledgement), uint32(Pass)) {
					select {
					case p.outcomes <- Observation{Mode: DropCommitAcknowledgement, Forwarded: true, ServerOK: packet[0] == 'C' && bytes.Equal(packet[5:], []byte("COMMIT\x00")), Suppressed: true}:
					default:
					}
					return
				}
			}
			if writePacket(client, packet) != nil {
				return
			}
		}
	}()
	for {
		packet, e := postgresPacket(client)
		if e != nil {
			break
		}
		if packet[0] == 'Q' && bytes.EqualFold(packet[5:], []byte("commit\x00")) {
			p.commits.Add(1)
			mode := Mode(p.mode.Swap(uint32(Pass)))
			if mode == DropBeforeCommit {
				select {
				case p.outcomes <- Observation{Mode: mode, Suppressed: true}:
				default:
				}
				break
			}
			if mode == DropCommitAcknowledgement {
				awaiting.Store(uint32(mode))
			}
		}
		if writePacket(upstream, packet) != nil {
			break
		}
	}
	upstream.Close()
	client.Close()
	<-done
}
func (p *PlaintextPostgreSQL) Close(ctx context.Context) error {
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
		return errors.New("POSTGRES_COMMIT_FAULT_SHUTDOWN_TIMEOUT")
	}
}
