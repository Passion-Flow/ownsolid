//go:build repository_integration && postgres_backup_integration

package postgresbackup

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// This isolated plaintext fixture proxy never logs authentication, SQL, bind
// parameters or row packets. It relays them unchanged, except for withholding
// one original authority transaction's successful COMMIT acknowledgement.
type commitFaultProxy struct {
	listener net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	active   map[net.Conn]struct{}
	proof    commitFaultProxyProof
}

type commitFaultProxyProof struct {
	AuthorityInsertObserved bool `json:"authority_insert_observed"`
	CommitRequestForwarded  bool `json:"commit_request_forwarded"`
	BackendCommitConsumed   bool `json:"backend_commit_consumed"`
	BackendPID              int  `json:"backend_pid"`
	DroppedAcknowledgements int  `json:"dropped_acknowledgements"`
}

func validCommitFaultProxyAddress(address string) bool {
	host, port, e := net.SplitHostPort(address)
	n, pe := strconv.Atoi(port)
	return e == nil && pe == nil && host == "127.0.0.1" && n >= 1024 && n <= 65535 && strconv.Itoa(n) == port
}

func newCommitFaultProxy(ctx context.Context, address string) (*commitFaultProxy, error) {
	if !validCommitFaultProxyAddress(address) {
		return nil, errors.New("M18_EXPLICIT_LOOPBACK_PROXY_REQUIRED")
	}
	listener, e := net.Listen("tcp4", address)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &commitFaultProxy{listener: listener, cancel: cancel, active: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			p.mu.Lock()
			if ctx.Err() != nil {
				p.mu.Unlock()
				client.Close()
				return
			}
			p.active[client] = struct{}{}
			p.mu.Unlock()
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.relay(ctx, client)
			}()
		}
	}()
	return p, nil
}

func (p *commitFaultProxy) close() {
	p.cancel()
	p.listener.Close()
	p.mu.Lock()
	for conn := range p.active {
		conn.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *commitFaultProxy) snapshot() commitFaultProxyProof {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.proof
}

func (p *commitFaultProxy) relay(ctx context.Context, client net.Conn) {
	defer func() {
		client.Close()
		p.mu.Lock()
		delete(p.active, client)
		p.mu.Unlock()
	}()
	upstream, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", "postgres:5432")
	if e != nil {
		return
	}
	p.mu.Lock()
	if ctx.Err() != nil {
		p.mu.Unlock()
		upstream.Close()
		return
	}
	p.active[upstream] = struct{}{}
	p.mu.Unlock()
	defer func() {
		upstream.Close()
		p.mu.Lock()
		delete(p.active, upstream)
		p.mu.Unlock()
	}()
	// PostgreSQL startup lacks a type byte. Relay optional encryption requests
	// and the server's exact one-byte answer before the ordinary startup frame.
	for {
		header := make([]byte, 4)
		if _, e = io.ReadFull(client, header); e != nil {
			return
		}
		size := binary.BigEndian.Uint32(header)
		if size < 8 || size > 64<<10 {
			return
		}
		payload := make([]byte, size-4)
		if _, e = io.ReadFull(client, payload); e != nil || writeCommitFaultPacket(upstream, append(header, payload...)) != nil {
			return
		}
		code := binary.BigEndian.Uint32(payload)
		if code == 80877103 || code == 80877104 {
			answer := []byte{0}
			if _, e = io.ReadFull(upstream, answer); e != nil || writeCommitFaultPacket(client, answer) != nil || answer[0] != 'N' {
				return
			}
			continue
		}
		if code == 80877102 { // Actual cancellation packets have no response.
			return
		}
		break
	}
	var stateMu sync.Mutex
	armed, commit, backendPID := false, false, 0
	done := make(chan struct{}, 2)
	go func() {
		defer func() { client.Close(); upstream.Close(); done <- struct{}{} }()
		for {
			kind, raw, payload, e := readCommitFaultPacket(client)
			if e != nil {
				return
			}
			query := ""
			if kind == 'Q' {
				query = strings.TrimRight(string(payload), "\x00")
			} else if kind == 'P' {
				if at := bytes.IndexByte(payload, 0); at >= 0 {
					remaining := payload[at+1:]
					if end := bytes.IndexByte(remaining, 0); end >= 0 {
						query = string(remaining[:end])
					}
				}
			}
			stateMu.Lock()
			if strings.Contains(query, "INSERT INTO public.audit_events(") && strings.Contains(query, "'operator.kubernetes_data_restored'") {
				armed = true
			}
			isCommit := armed && strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(query, ";")), "commit")
			if isCommit {
				// Set before the write: the backend can answer immediately after
				// receiving it. A consumed COMMIT tag independently proves delivery.
				commit = true
			}
			stateMu.Unlock()
			if writeCommitFaultPacket(upstream, raw) != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { client.Close(); upstream.Close(); done <- struct{}{} }()
		for {
			kind, raw, payload, e := readCommitFaultPacket(upstream)
			if e != nil {
				return
			}
			stateMu.Lock()
			if kind == 'K' && len(payload) >= 8 {
				backendPID = int(binary.BigEndian.Uint32(payload[:4]))
			}
			drop := armed && commit && kind == 'C' && bytes.Equal(payload, []byte("COMMIT\x00"))
			if drop {
				p.mu.Lock()
				if p.proof.DroppedAcknowledgements == 0 {
					p.proof = commitFaultProxyProof{true, true, true, backendPID, 1}
				} else {
					drop = false
				}
				p.mu.Unlock()
			}
			stateMu.Unlock()
			if drop || writeCommitFaultPacket(client, raw) != nil {
				return
			}
		}
	}()
	<-done
	<-done
}

func readCommitFaultPacket(reader io.Reader) (byte, []byte, []byte, error) {
	header := make([]byte, 5)
	if _, e := io.ReadFull(reader, header); e != nil {
		return 0, nil, nil, e
	}
	size := binary.BigEndian.Uint32(header[1:])
	if size < 4 || size > 16<<20 {
		return 0, nil, nil, errors.New("M18_PROXY_PACKET_BOUND_REQUIRED")
	}
	raw := make([]byte, 5+int(size)-4)
	copy(raw, header)
	if _, e := io.ReadFull(reader, raw[5:]); e != nil {
		return 0, nil, nil, e
	}
	return header[0], raw, raw[5:], nil
}

func writeCommitFaultPacket(writer io.Writer, raw []byte) error {
	for len(raw) > 0 {
		n, e := writer.Write(raw)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}
