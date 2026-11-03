// Package modelcache implements the bounded single-instance Valkey RESP2 subset.
package modelcache

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

var ErrUnavailable = errors.New("MODEL_CACHE_UNAVAILABLE")
var ErrInvalid = errors.New("MODEL_CACHE_PAYLOAD_INVALID")

type Client struct {
	policy   config.ModelCacheConfiguration
	password []byte
	tls      *tls.Config
	slots    chan struct{}
}

// New validates local inputs only. Optional dependency availability never gates startup.
func New(c config.ModelCacheConfiguration) (*Client, error) {
	if c == (config.ModelCacheConfiguration{}) {
		return nil, nil
	}
	if c.Validate() != nil {
		return nil, errors.New("CONFIG_MODEL_CACHE_INVALID")
	}
	if !c.Enabled {
		return nil, nil
	}
	password, e := c.Password()
	if e != nil || len(password) == 0 || len(password) > 4096 {
		return nil, errors.New("CONFIG_MODEL_CACHE_SECRET_INVALID")
	}
	password = []byte(strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r"))
	if len(password) == 0 || strings.ContainsAny(string(password), "\r\n") {
		return nil, errors.New("CONFIG_MODEL_CACHE_SECRET_INVALID")
	}
	client := &Client{policy: c, password: password, slots: make(chan struct{}, c.MaximumConcurrent)}
	if c.TLS {
		client.tls = &tls.Config{MinVersion: tls.VersionTLS12}
		if c.CAFile != "" {
			file, e := os.Open(c.CAFile)
			if e != nil {
				return nil, errors.New("CONFIG_MODEL_CACHE_CA_INVALID")
			}
			raw, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
			closeError := file.Close()
			if e != nil || closeError != nil || len(raw) > 1<<20 {
				return nil, errors.New("CONFIG_MODEL_CACHE_CA_INVALID")
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(raw) {
				return nil, errors.New("CONFIG_MODEL_CACHE_CA_INVALID")
			}
			client.tls.RootCAs = roots
		}
	}
	return client, nil
}
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	return c.execute(ctx, []byte("GET"), []byte(c.policy.Namespace+":"+key))
}
func (c *Client) Set(ctx context.Context, key string, value []byte) error {
	if c == nil || len(value) > c.policy.MaximumBytes {
		return ErrInvalid
	}
	result, e := c.execute(ctx, []byte("SET"), []byte(c.policy.Namespace+":"+key), value, []byte("PX"), []byte(strconv.Itoa(c.policy.TTLSeconds*1000)))
	if e != nil {
		return e
	}
	if string(result) != "OK" {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) execute(ctx context.Context, args ...[]byte) ([]byte, error) {
	if c == nil || ctx == nil {
		return nil, ErrUnavailable
	}
	operationBudget := time.Duration(c.policy.TimeoutMilliseconds) * time.Millisecond
	// Optional cache work must leave an equally sized budget for source fallback.
	// A nearly expired caller skips the cache instead of spending its last deadline.
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= operationBudget+operationBudget {
		return nil, ErrUnavailable
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, operationBudget)
	defer cancel()
	var conn net.Conn
	var e error
	dialer := &net.Dialer{}
	if c.tls != nil {
		conn, e = (&tls.Dialer{NetDialer: dialer, Config: c.tls}).DialContext(ctx, "tcp", c.policy.Address)
	} else {
		conn, e = dialer.DialContext(ctx, "tcp", c.policy.Address)
	}
	if e != nil {
		return nil, ErrUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return nil, ErrUnavailable
	}
	reader := bufio.NewReaderSize(conn, 4096)
	if writeCommand(conn, []byte("AUTH"), []byte(c.policy.Username), c.password) != nil {
		return nil, ErrUnavailable
	}
	authenticated, e := readCommandReply(reader, 16, '+')
	if e != nil || string(authenticated) != "OK" {
		return nil, ErrUnavailable
	}
	if writeCommand(conn, args...) != nil {
		return nil, ErrUnavailable
	}
	want := byte('$') // GET returns a bulk value or a null bulk miss.
	if string(args[0]) == "SET" {
		want = '+' // SET without conditional options returns the simple string OK.
	}
	result, e := readCommandReply(reader, c.policy.MaximumBytes, want)
	if e != nil {
		return nil, ErrUnavailable
	}
	return result, nil
}

// A syntactically valid RESP value is not necessarily a valid command reply.
// Keep AUTH/SET acknowledgements distinct from GET payloads, including "OK".
func readCommandReply(r *bufio.Reader, maximum int, want byte) ([]byte, error) {
	kind, e := r.Peek(1)
	if e != nil || kind[0] != want {
		return nil, ErrInvalid
	}
	return readReply(r, maximum)
}

func writeCommand(w io.Writer, args ...[]byte) error {
	b := bufio.NewWriter(w)
	if _, e := b.WriteString("*" + strconv.Itoa(len(args)) + "\r\n"); e != nil {
		return e
	}
	for _, a := range args {
		if _, e := b.WriteString("$" + strconv.Itoa(len(a)) + "\r\n"); e != nil {
			return e
		}
		if _, e := b.Write(a); e != nil {
			return e
		}
		if _, e := b.WriteString("\r\n"); e != nil {
			return e
		}
	}
	return b.Flush()
}
func readReply(r *bufio.Reader, maximum int) ([]byte, error) {
	line, e := r.ReadSlice('\n')
	if e != nil || len(line) < 3 || len(line) > 128 || line[len(line)-2] != '\r' {
		return nil, ErrInvalid
	}
	line = line[:len(line)-2]
	switch line[0] {
	case '+':
		if string(line) != "+OK" {
			return nil, ErrInvalid
		}
		return []byte("OK"), nil
	case '$':
		if string(line) == "$-1" {
			return nil, nil
		}
		n, e := strconv.Atoi(string(line[1:]))
		if e != nil || n < 0 || n > maximum {
			return nil, ErrInvalid
		}
		body := make([]byte, n+2)
		if _, e = io.ReadFull(r, body); e != nil || body[n] != '\r' || body[n+1] != '\n' {
			return nil, ErrInvalid
		}
		return body[:n], nil
	default:
		return nil, ErrInvalid
	}
}
