//go:build repository_integration && task_events_integration && task_events_wire_integration

package taskbus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maximumWireFixtureFrame = 1 << 20
const maximumWireFixtureConnections = 8

type WireObservation struct {
	Dropped, PublishAttempts, PublishForwarded, AckAttempts, AckForwarded int
	Invalid                                                               bool
}
type WireFixture struct {
	mu                     sync.Mutex
	observation            WireObservation
	armed                  string
	configuration          Configuration
	ctx                    context.Context
	cancel                 context.CancelFunc
	listener               net.Listener
	connections            map[net.Conn]bool
	active                 sync.WaitGroup
	capacity               chan struct{}
	serverTLS, upstreamTLS *tls.Config
}

// NewWireFixture creates only an owned loopback TLS fault proxy for a real
// existing private NATS backend. It never supplies a server/API/task result.
// NKey challenges and messages pass unchanged to the actual broker. Caller must
// stop/join this fixture before deleting its exclusively owned root.
func NewWireFixture(ctx context.Context, c Configuration, root string) (*WireFixture, Configuration, error) {
	if ctx == nil || c.Validate() != nil || !c.Enabled {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_INVALID")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root || !filepath.IsAbs(root) {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_ROOT_INVALID")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_ROOT_INVALID")
	}
	upstream, err := tlsConfiguration(c)
	if err != nil {
		return nil, Configuration{}, err
	}
	u, _ := url.Parse(c.Endpoint)
	upstream.ServerName = u.Hostname()
	certificate, err := x509.ParseCertificate(upstream.Certificates[0].Certificate[0])
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CERTIFICATE_INVALID")
	}
	pin := sha256.Sum256(certificate.Raw)
	now := time.Now().UTC()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Synthetic task event wire CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	serial, err = rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	server := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Synthetic loopback task event wire proxy"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	serverDER, err := x509.CreateCertificate(rand.Reader, server, ca, &serverKey.PublicKey, key)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	private, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	defer wipe(private)
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CRYPTO_FAILED")
	}
	caPath := filepath.Join(root, "wire-ca.pem")
	file, err := os.OpenFile(caPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CA_WRITE_FAILED")
	}
	_, writeErr := file.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CA_WRITE_FAILED")
	}
	directory, err := os.Open(root)
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CA_WRITE_FAILED")
	}
	syncErr = directory.Sync()
	closeErr = directory.Close()
	if syncErr != nil || closeErr != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_CA_WRITE_FAILED")
	}
	serverTLS := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAnyClientCert, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) < 1 || sha256.Sum256(state.PeerCertificates[0].Raw) != pin || time.Now().Before(state.PeerCertificates[0].NotBefore) || !time.Now().Before(state.PeerCertificates[0].NotAfter) {
			return errors.New("WIRE_FIXTURE_CLIENT_REJECTED")
		}
		return nil
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_LISTENER_FAILED")
	}
	run, cancel := context.WithCancel(ctx)
	p := &WireFixture{configuration: c, ctx: run, cancel: cancel, listener: listener, connections: map[net.Conn]bool{}, capacity: make(chan struct{}, maximumWireFixtureConnections), serverTLS: serverTLS, upstreamTLS: upstream}
	p.active.Add(1)
	go p.accept()
	selected := c
	selected.Endpoint = "tls://" + listener.Addr().String()
	selected.DialAddresses = []string{"127.0.0.1"}
	selected.CAFile = caPath
	if selected.Validate() != nil {
		stop, end := context.WithTimeout(context.Background(), time.Second)
		defer end()
		_ = p.Close(stop)
		return nil, Configuration{}, errors.New("WIRE_FIXTURE_SELECTED_PROFILE_INVALID")
	}
	return p, selected, nil
}
func (p *WireFixture) Arm(mode string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.armed != "" || p.observation.Dropped != 0 {
		return errors.New("WIRE_FIXTURE_ALREADY_ARMED")
	}
	switch mode {
	case "publish_ack_lost", "publish_send_cut", "ack_confirmation_lost", "ack_send_cut":
		p.armed = mode
		return nil
	}
	return errors.New("WIRE_FIXTURE_MODE_INVALID")
}
func (p *WireFixture) Observation() WireObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observation
}
func (p *WireFixture) track(c net.Conn, add bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if add {
		p.connections[c] = true
	} else {
		delete(p.connections, c)
	}
}
func (p *WireFixture) invalid() { p.mu.Lock(); p.observation.Invalid = true; p.mu.Unlock() }
func (p *WireFixture) accept() {
	defer p.active.Done()
	for {
		c, err := p.listener.Accept()
		if err != nil {
			return
		}
		select {
		case p.capacity <- struct{}{}:
		default:
			c.Close()
			p.invalid()
			continue
		}
		p.track(c, true)
		p.active.Add(1)
		go func() {
			defer p.active.Done()
			defer func() { <-p.capacity }()
			defer p.track(c, false)
			defer c.Close()
			p.serve(c)
		}()
	}
}
func (p *WireFixture) serve(raw net.Conn) {
	downstream := tls.Server(raw, p.serverTLS)
	bounded, cancel := context.WithTimeout(p.ctx, time.Duration(p.configuration.ConnectSeconds+p.configuration.RequestSeconds)*time.Second)
	defer cancel()
	if downstream.HandshakeContext(bounded) != nil {
		return
	}
	u, _ := url.Parse(p.configuration.Endpoint)
	dialer := pinnedDialer{ctx: bounded, authority: u.Host, port: u.Port(), addresses: p.configuration.DialAddresses, timeout: time.Duration(p.configuration.ConnectSeconds) * time.Second}
	base, err := dialer.Dial("tcp", u.Host)
	if err != nil {
		return
	}
	defer base.Close()
	p.track(base, true)
	defer p.track(base, false)
	upstream := tls.Client(base, p.upstreamTLS.Clone())
	if upstream.HandshakeContext(bounded) != nil {
		return
	}
	var pendingMu sync.Mutex
	pending := map[string]string{}
	done := make(chan struct{}, 2)
	var closing atomic.Bool
	pump := func(from, to net.Conn, client bool) {
		defer func() { closing.Store(true); downstream.Close(); upstream.Close(); done <- struct{}{} }()
		reader := bufio.NewReaderSize(from, 64<<10)
		for {
			frame, e := readWireFrame(reader)
			if e != nil {
				if !closing.Load() && !errors.Is(e, io.EOF) && !errors.Is(e, net.ErrClosed) && p.ctx.Err() == nil {
					p.invalid()
				}
				return
			}
			kind := ""
			if client && (frame.command == "PUB" || frame.command == "HPUB") {
				if strings.HasPrefix(frame.subject, subjectPrefix+"."+wire.DefaultSiteID+"."+wire.DefaultCellID+"."+p.configuration.NamespaceID+".") {
					kind = "publish"
				}
				if strings.HasPrefix(frame.subject, "$JS.ACK.") && strings.Contains(frame.subject, "."+p.configuration.Stream+"."+p.configuration.Consumer+".") && bytes.Equal(frame.payload(), []byte("+ACK")) {
					kind = "ack"
				}
				p.mu.Lock()
				if kind == "publish" {
					p.observation.PublishAttempts++
				}
				if kind == "ack" {
					p.observation.AckAttempts++
				}
				cut := p.armed == "publish_send_cut" && kind == "publish" || p.armed == "ack_send_cut" && kind == "ack"
				if cut {
					p.armed = ""
					p.observation.Dropped++
				}
				p.mu.Unlock()
				if cut {
					return
				}
				if kind != "" && frame.reply != "" {
					pendingMu.Lock()
					if len(pending) >= 64 {
						pendingMu.Unlock()
						p.invalid()
						return
					}
					pending[frame.reply] = kind
					pendingMu.Unlock()
				}
			}
			if !client && (frame.command == "MSG" || frame.command == "HMSG") {
				pendingMu.Lock()
				responseKind := pending[frame.subject]
				delete(pending, frame.subject)
				pendingMu.Unlock()
				valid := false
				if responseKind == "publish" {
					var ack struct {
						Stream   string          `json:"stream"`
						Sequence uint64          `json:"seq"`
						Error    json.RawMessage `json:"error"`
					}
					valid = json.Unmarshal(frame.payload(), &ack) == nil && ack.Stream == p.configuration.Stream && ack.Sequence > 0 && (len(ack.Error) == 0 || string(ack.Error) == "null")
				}
				if responseKind == "ack" {
					valid = frame.command == "MSG" && len(frame.payload()) == 0
				}
				p.mu.Lock()
				drop := valid && (p.armed == "publish_ack_lost" && responseKind == "publish" || p.armed == "ack_confirmation_lost" && responseKind == "ack")
				if drop {
					p.armed = ""
					p.observation.Dropped++
				}
				p.mu.Unlock()
				if drop {
					return
				}
			}
			if writeErr := writeWireFrame(to, frame.raw); writeErr != nil {
				return
			}
			if client && kind != "" {
				p.mu.Lock()
				if kind == "publish" {
					p.observation.PublishForwarded++
				} else {
					p.observation.AckForwarded++
				}
				p.mu.Unlock()
			}
		}
	}
	go pump(downstream, upstream, true)
	go pump(upstream, downstream, false)
	<-done
	<-done
}
func (p *WireFixture) Close(ctx context.Context) error {
	if p == nil || ctx == nil {
		return errors.New("WIRE_FIXTURE_INVALID")
	}
	p.cancel()
	p.listener.Close()
	p.mu.Lock()
	connections := make([]net.Conn, 0, len(p.connections))
	for c := range p.connections {
		connections = append(connections, c)
	}
	p.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
	done := make(chan struct{})
	go func() { p.active.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return errors.New("WIRE_FIXTURE_STOP_UNCONFIRMED")
	case <-done:
		return nil
	}
}

type wireFrame struct {
	command, subject, reply string
	raw, body               []byte
	header                  int
}

func (f wireFrame) payload() []byte { return f.body[f.header:] }
func readWireFrame(r *bufio.Reader) (wireFrame, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return wireFrame{}, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	parts := strings.Fields(string(line))
	if len(parts) < 1 {
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	f := wireFrame{command: parts[0], raw: append([]byte(nil), line...)}
	minimum, maximum := 0, 0
	switch f.command {
	case "PUB":
		minimum, maximum = 3, 4
	case "HPUB":
		minimum, maximum = 4, 5
	case "MSG":
		minimum, maximum = 4, 5
	case "HMSG":
		minimum, maximum = 5, 6
	case "CONNECT", "INFO", "PING", "PONG", "SUB", "UNSUB", "+OK", "-ERR":
		return f, nil
	default:
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	if len(parts) < minimum || len(parts) > maximum {
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	f.subject = parts[1]
	length, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || length < 0 || length > maximumWireFixtureFrame {
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	if f.command == "HPUB" || f.command == "HMSG" {
		f.header, err = strconv.Atoi(parts[len(parts)-2])
		if err != nil || f.header < 0 || f.header > length {
			return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
		}
	}
	if len(parts) == maximum {
		if f.command == "MSG" || f.command == "HMSG" {
			f.reply = parts[3]
		} else {
			f.reply = parts[2]
		}
	}
	body := make([]byte, length+2)
	if _, err = io.ReadFull(r, body); err != nil {
		return wireFrame{}, err
	}
	if !bytes.Equal(body[length:], []byte("\r\n")) {
		return wireFrame{}, errors.New("WIRE_FIXTURE_FRAME_INVALID")
	}
	f.body = body[:length]
	f.raw = append(f.raw, body...)
	return f, nil
}
func writeWireFrame(w io.Writer, raw []byte) error {
	for len(raw) > 0 {
		n, err := w.Write(raw)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}
