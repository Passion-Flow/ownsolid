//go:build repository_integration && postgres_target_initialization_integration

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This test-only plaintext PostgreSQLv3 proxy has no SQL authority or client
// implementation. Authentication/query frames are opaque and never logged.
// Only one actual SimpleQuery COMMIT acknowledgement is dropped; no server
// frame, result, row, command tag or successful transaction is synthesized.
type targetInitializationCommitProxy struct {
	listener    net.Listener
	upstream    string
	ctx         context.Context
	cancel      context.CancelFunc
	wait        sync.WaitGroup
	mutex       sync.Mutex
	connections map[net.Conn]bool
	claimed     atomic.Bool
	commits     atomic.Int32
	dropped     atomic.Int32
	accepted    atomic.Int32
	overflow    atomic.Bool
}

func newTargetInitializationCommitProxy(t *testing.T, upstream string) *targetInitializationCommitProxy {
	t.Helper()
	host, _, e := net.SplitHostPort(upstream)
	if e != nil || host != "127.0.0.1" {
		t.Fatal("fixed loopback upstream required")
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("bounded local fault listener unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	p := &targetInitializationCommitProxy{listener: listener, upstream: upstream, ctx: ctx, cancel: cancel, connections: map[net.Conn]bool{}}
	p.wait.Add(2)
	go func() { defer p.wait.Done(); <-ctx.Done(); p.closeIO() }()
	go func() {
		defer p.wait.Done()
		for {
			client, e := listener.Accept()
			if e != nil {
				return
			}
			if p.accepted.Add(1) > 4 {
				p.overflow.Store(true)
				client.Close()
				cancel()
				return
			}
			if !p.track(client) {
				client.Close()
				return
			}
			p.wait.Add(1)
			go p.serve(client)
		}
	}()
	t.Cleanup(func() {
		cancel()
		p.closeIO()
		joined := make(chan struct{})
		go func() { p.wait.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("fault proxy cleanup did not join")
		}
		if p.overflow.Load() {
			t.Error("fault proxy exceeded four-connection inventory")
		}
	})
	return p
}

func (p *targetInitializationCommitProxy) track(connection net.Conn) bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.ctx.Err() != nil {
		return false
	}
	p.connections[connection] = true
	if deadline, ok := p.ctx.Deadline(); ok {
		connection.SetDeadline(deadline)
	}
	return true
}
func (p *targetInitializationCommitProxy) release(connection net.Conn) {
	connection.Close()
	p.mutex.Lock()
	delete(p.connections, connection)
	p.mutex.Unlock()
}
func (p *targetInitializationCommitProxy) closeIO() {
	p.listener.Close()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for connection := range p.connections {
		connection.Close()
	}
}
func targetInitializationProxyWrite(connection net.Conn, frame []byte) error {
	for len(frame) > 0 {
		n, e := connection.Write(frame)
		if e != nil {
			return e
		}
		if n < 1 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func targetInitializationProxyFrame(connection net.Conn, startup bool) ([]byte, error) {
	headerSize := 5
	if startup {
		headerSize = 4
	}
	header := make([]byte, headerSize)
	if _, e := io.ReadFull(connection, header); e != nil {
		return nil, e
	}
	lengthOffset := headerSize - 4
	length := binary.BigEndian.Uint32(header[lengthOffset:])
	if length < 4 || length > (1<<20)-uint32(lengthOffset) {
		return nil, errors.New("FAULT_PROXY_FRAME_BOUND")
	}
	frame := make([]byte, int(length)+lengthOffset)
	copy(frame, header)
	if _, e := io.ReadFull(connection, frame[headerSize:]); e != nil {
		return nil, e
	}
	if startup && (len(frame) < 8 || binary.BigEndian.Uint32(frame[4:8]) != 196608) {
		return nil, errors.New("FAULT_PROXY_PLAINTEXT_V3_REQUIRED")
	}
	return frame, nil
}
func (p *targetInitializationCommitProxy) serve(client net.Conn) {
	defer p.wait.Done()
	defer p.release(client)
	startup, e := targetInitializationProxyFrame(client, true)
	if e != nil {
		return
	}
	server, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(p.ctx, "tcp4", p.upstream)
	if e != nil {
		return
	}
	if !p.track(server) {
		server.Close()
		return
	}
	defer p.release(server)
	if targetInitializationProxyWrite(server, startup) != nil {
		return
	}
	var selected atomic.Bool
	joined := make(chan struct{}, 2)
	go func() {
		defer func() { joined <- struct{}{} }()
		for {
			frame, e := targetInitializationProxyFrame(client, false)
			if e != nil {
				return
			}
			if frame[0] == 'Q' && len(frame) > 5 && frame[len(frame)-1] == 0 && strings.EqualFold(strings.TrimSpace(string(frame[5:len(frame)-1])), "COMMIT") {
				p.commits.Add(1)
				if p.claimed.CompareAndSwap(false, true) {
					selected.Store(true)
				}
			}
			if targetInitializationProxyWrite(server, frame) != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { joined <- struct{}{} }()
		for {
			frame, e := targetInitializationProxyFrame(server, false)
			if e != nil {
				return
			}
			if selected.Load() && frame[0] == 'C' && bytes.Equal(frame[5:], []byte("COMMIT\x00")) {
				// PostgreSQL has acknowledged the actual commit. Neither this C
				// nor its following ReadyForQuery reaches the original client.
				p.dropped.Add(1)
				return
			}
			if targetInitializationProxyWrite(client, frame) != nil {
				return
			}
		}
	}()
	<-joined
	client.Close()
	server.Close()
	<-joined
}
