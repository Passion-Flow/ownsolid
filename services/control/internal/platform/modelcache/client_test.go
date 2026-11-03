package modelcache

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func testPolicy(t *testing.T) config.ModelCacheConfiguration {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture root")
	}
	path := filepath.Join(root, "password")
	if e = os.WriteFile(path, []byte("synthetic-password"), 0600); e != nil {
		t.Fatal(e)
	}
	c := config.DefaultModelCacheConfiguration()
	c.Enabled = true
	c.TLS = false
	c.Username = "synthetic"
	c.PasswordFile = path
	c.Address = "127.0.0.1:1"
	c.TimeoutMilliseconds = 300
	c.TTLSeconds = 17
	c.Namespace = "synthetic"
	return c
}
func readCommand(t *testing.T, r *bufio.Reader) [][]byte {
	t.Helper()
	line, e := r.ReadString('\n')
	if e != nil || len(line) < 4 || line[0] != '*' {
		t.Error("command array")
		return nil
	}
	n, e := strconv.Atoi(strings.TrimSpace(line[1:]))
	if e != nil || n < 1 || n > 5 {
		t.Error("command length")
		return nil
	}
	values := make([][]byte, n)
	for i := range values {
		line, e = r.ReadString('\n')
		if e != nil || len(line) < 4 || line[0] != '$' {
			t.Error("command bulk")
			return nil
		}
		size, e := strconv.Atoi(strings.TrimSpace(line[1:]))
		if e != nil || size < 0 || size > 1<<20 {
			t.Error("bulk length")
			return nil
		}
		raw := make([]byte, size+2)
		if _, e = io.ReadFull(r, raw); e != nil || !bytes.HasSuffix(raw, []byte("\r\n")) {
			t.Error("command bytes")
			return nil
		}
		values[i] = raw[:size]
	}
	return values
}
func TestValkeyProtocolRoundTrip(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			r := bufio.NewReader(conn)
			auth := readCommand(t, r)
			if len(auth) != 3 || string(auth[0]) != "AUTH" || string(auth[1]) != "synthetic" || string(auth[2]) != "synthetic-password" {
				t.Error("exact auth")
			}
			_, _ = io.WriteString(conn, "+OK\r\n")
			command := readCommand(t, r)
			if len(command) < 2 || string(command[1]) != "synthetic:model-directory:key" {
				t.Error("namespace")
			}
			if i == 0 {
				if len(command) != 5 || string(command[0]) != "SET" || string(command[2]) != "payload" || string(command[3]) != "PX" || string(command[4]) != "17000" {
					t.Error("SET PX")
				}
				_, _ = io.WriteString(conn, "+OK\r\n")
			} else if i == 1 {
				_, _ = io.WriteString(conn, "$7\r\npayload\r\n")
			} else {
				_, _ = io.WriteString(conn, "$-1\r\n")
			}
			conn.Close()
		}
	}()
	c := testPolicy(t)
	c.Address = listener.Addr().String()
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Set(t.Context(), "model-directory:key", []byte("payload")); e != nil {
		t.Fatal(e)
	}
	raw, e := client.Get(t.Context(), "model-directory:key")
	if e != nil || string(raw) != "payload" {
		t.Fatal("GET", e)
	}
	raw, e = client.Get(t.Context(), "model-directory:key")
	if e != nil || raw != nil {
		t.Fatal("miss", e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("listener did not exit")
	}
}
func TestValkeyBoundedFailure(t *testing.T) {
	for _, raw := range []string{"$1025\r\n", "$-2\r\n", "$abc\r\n", "$3\r\nabcxx", "+OK\n", "-ERR synthetic-secret\r\n", "*1\r\n", "$3\r\nab", strings.Repeat("a", 4096)} {
		if _, e := readReply(bufio.NewReader(strings.NewReader(raw)), 1024); e == nil {
			t.Fatal("malformed reply accepted")
		}
	}
	c := testPolicy(t)
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Get(t.Context(), "key"); e != ErrUnavailable {
		t.Fatal("unavailable unsanitized", e)
	}
	if e = client.Set(t.Context(), "key", make([]byte, c.MaximumBytes+1)); e != ErrInvalid {
		t.Fatal("oversize accepted")
	}
	for i := 0; i < c.MaximumConcurrent; i++ {
		client.slots <- struct{}{}
	}
	if _, e = client.Get(t.Context(), "key"); e != ErrUnavailable {
		t.Fatal("busy accepted")
	}
	for i := 0; i < c.MaximumConcurrent; i++ {
		<-client.slots
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = client.Get(ctx, "key"); e != ErrUnavailable {
		t.Fatal("cancelled")
	}
	server := httptest.NewTLSServer(nil)
	defer server.Close()
	c.TLS = true
	c.Address = strings.TrimPrefix(server.URL, "https://")
	client, e = New(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Get(t.Context(), "key"); e != ErrUnavailable {
		t.Fatal("untrusted TLS")
	}
	c = config.DefaultModelCacheConfiguration()
	c.PasswordFile = ""
	client, e = New(c)
	if e != nil || client != nil {
		t.Fatal("disabled read secrets")
	}
}
func TestValkeyTimeoutClosesConnection(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = io.Copy(io.Discard, conn)
	}()
	c := testPolicy(t)
	c.Address = listener.Addr().String()
	c.TimeoutMilliseconds = 20
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = client.Get(t.Context(), "key"); e != ErrUnavailable {
		t.Fatal("timeout", e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out connection leaked")
	}
}

func TestValkeyVerifiedTLS(t *testing.T) {
	source := httptest.NewTLSServer(nil)
	serverTLS := source.TLS.Clone()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: source.Certificate().Raw})
	source.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	listener = tls.NewListener(listener, serverTLS)
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		r := bufio.NewReader(conn)
		readCommand(t, r)
		_, _ = io.WriteString(conn, "+OK\r\n")
		readCommand(t, r)
		_, _ = io.WriteString(conn, "$6\r\nsecure\r\n")
	}()
	c := testPolicy(t)
	c.TLS = true
	c.Address = listener.Addr().String()
	c.CAFile = filepath.Join(filepath.Dir(c.PasswordFile), "ca.pem")
	if os.WriteFile(c.CAFile, ca, 0600) != nil {
		t.Fatal("CA fixture")
	}
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := client.Get(t.Context(), "key")
	if e != nil || string(raw) != "secure" {
		t.Fatal("verified TLS", e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TLS listener leaked")
	}
}
func TestValkeyPrivateSecretAdmission(t *testing.T) {
	c := testPolicy(t)
	if os.Chmod(c.PasswordFile, 0644) != nil {
		t.Fatal("fixture")
	}
	if _, e := New(c); e == nil {
		t.Fatal("public credential accepted")
	}
	if os.Chmod(c.PasswordFile, 0600) != nil {
		t.Fatal("fixture")
	}
	alias := filepath.Join(filepath.Dir(c.PasswordFile), "alias")
	if os.Symlink(c.PasswordFile, alias) != nil {
		t.Fatal("fixture")
	}
	c.PasswordFile = alias
	if _, e := New(c); e == nil {
		t.Fatal("symlink credential accepted")
	}
}

func TestValkeyPreservesSourceDeadline(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	accepted := make(chan bool, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			accepted <- false
			return
		}
		conn.Close()
		accepted <- true
	}()
	c := testPolicy(t)
	c.Address = listener.Addr().String()
	c.TimeoutMilliseconds = 300
	client, e := New(c)
	if e != nil {
		listener.Close()
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, e = client.Get(ctx, "key"); e != ErrUnavailable {
		t.Fatal("cache did not decline short budget", e)
	}
	listener.Close()
	if <-accepted {
		t.Fatal("optional cache consumed source fallback deadline")
	}
	if ctx.Err() != nil {
		t.Fatal("source deadline exhausted")
	}
}

func TestValkeyCommandReplyTypes(t *testing.T) {
	for _, tc := range []struct {
		name, auth, reply string
		set, valid        bool
	}{
		{"get-bulk-OK", "+OK\r\n", "$2\r\nOK\r\n", false, true},
		{"get-miss", "+OK\r\n", "$-1\r\n", false, true},
		{"get-simple", "+OK\r\n", "+OK\r\n", false, false},
		{"auth-bulk", "$2\r\nOK\r\n", "", false, false},
		{"set-bulk", "+OK\r\n", "$2\r\nOK\r\n", true, false},
		{"set-null", "+OK\r\n", "$-1\r\n", true, false},
		{"set-simple", "+OK\r\n", "+OK\r\n", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(time.Second))
				r := bufio.NewReader(conn)
				readCommand(t, r)
				_, _ = io.WriteString(conn, tc.auth)
				if tc.reply != "" {
					readCommand(t, r)
					_, _ = io.WriteString(conn, tc.reply)
				}
			}()
			c := testPolicy(t)
			c.Address = listener.Addr().String()
			client, e := New(c)
			if e != nil {
				t.Fatal(e)
			}
			if tc.set {
				e = client.Set(t.Context(), "key", []byte("payload"))
			} else {
				_, e = client.Get(t.Context(), "key")
			}
			if (e == nil) != tc.valid || e != nil && e != ErrUnavailable {
				t.Fatal("command reply type acceptance or sanitized fallback", e)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("reply fixture did not join")
			}
		})
	}
}
