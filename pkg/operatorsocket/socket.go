// Package operatorsocket is the Unix socket an operator on the host uses when nothing else reaches
// control (ADR 0043). It takes no TCP connection, TLS session, request slot or pooled database
// connection, so it answers with the connection limit full and the database stopped. File
// permissions are the credential: the socket is mode 0600 in a directory only its owner can enter.
//
// The server and the client live together so that control and ownsolid-install share one contract.
package operatorsocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	shedPath = "/v1/agents/shed"
	heldName = "agent-shed.json"

	// A reply carries a level, so a request or a reply larger than this is not ours.
	maximumBodyBytes   = 4 << 10
	maximumHeaderBytes = 8 << 10

	// StoreBudget is how long a request may wait for the database. The level already acts by then,
	// so the wait only decides whether the reply can say it was stored.
	StoreBudget = 5 * time.Second

	readTimeout     = 5 * time.Second
	writeTimeout    = 2 * StoreBudget
	shutdownTimeout = 2 * time.Second
	clientTimeout   = writeTimeout + readTimeout

	// ReasonUnavailable means no control process answered on the socket.
	ReasonUnavailable = "OPERATOR_SOCKET_UNAVAILABLE"
	// ReasonInvalidReply means something answered that does not speak this contract.
	ReasonInvalidReply = "OPERATOR_SOCKET_REPLY_INVALID"
	// ReasonDirectoryInvalid refuses a socket that someone other than its owner could reach.
	ReasonDirectoryInvalid = "OPERATOR_SOCKET_DIRECTORY_INVALID"
	// ReasonListenFailed means control could not bind the socket.
	ReasonListenFailed = "OPERATOR_SOCKET_LISTEN_FAILED"
)

// ShedRequest asks control to refuse agent traffic down to Level.
type ShedRequest struct {
	Level  string `json:"level"`
	Reason string `json:"reason"`
}

// ShedStatus is the level in force in the control process. Stored is false while the database has
// not recorded it: a restart before it does forgets the level.
type ShedStatus struct {
	Level  string `json:"level"`
	Stored bool   `json:"stored"`
}

// Levels are the names a request may carry, lowest impact first.
var Levels = []string{"none", "bulk", "telemetry", "heartbeats"}

// maximumReasonRunes is what an audit record keeps.
const maximumReasonRunes = 500

// Valid reports whether control would accept r, for a caller that cannot ask it.
func (r ShedRequest) Valid() bool {
	return slices.Contains(Levels, r.Level) && utf8.ValidString(r.Reason) && !strings.ContainsRune(r.Reason, 0) &&
		strings.TrimSpace(r.Reason) != "" && utf8.RuneCountInString(r.Reason) <= maximumReasonRunes
}

// failure is the body of every refused request.
type failure struct {
	Code string `json:"code"`
}

// Control is what the socket exposes. A refused request returns an error whose text is a stable code.
type Control interface {
	LocalShed() ShedStatus
	SetLocalShed(context.Context, ShedRequest) (ShedStatus, error)
}

// Server serves one socket.
type Server struct {
	listener *net.UnixListener
	server   *http.Server
}

// Listen binds path for control. A socket left by a killed process is replaced; anything else at
// path, or a directory others can enter, refuses to start.
func Listen(path string, control Control) (*Server, error) {
	directory, e := os.Lstat(filepath.Dir(path))
	if e != nil || !directory.IsDir() || directory.Mode().Perm()&0o077 != 0 {
		return nil, errors.New(ReasonDirectoryInvalid)
	}
	if existing, e := os.Lstat(path); e == nil {
		if existing.Mode().Type() != fs.ModeSocket || os.Remove(path) != nil {
			return nil, errors.New(ReasonListenFailed)
		}
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return nil, errors.New(ReasonListenFailed)
	}
	// A newer process may already own the path when this one closes, and Listen removes what a
	// process leaves behind.
	listener.SetUnlinkOnClose(false)
	if e = os.Chmod(path, 0o600); e != nil {
		_ = listener.Close()
		return nil, errors.New(ReasonListenFailed)
	}
	return &Server{listener: listener, server: &http.Server{
		Handler:           handler(control),
		ReadHeaderTimeout: readTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		MaxHeaderBytes:    maximumHeaderBytes,
	}}, nil
}

// Serve answers until ctx ends, then finishes what is in flight.
func (s *Server) Serve(ctx context.Context) error {
	finished := make(chan error, 1)
	go func() { finished <- s.server.Serve(s.listener) }()
	select {
	case e := <-finished:
		return e
	case <-ctx.Done():
	}
	stopping, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	e := s.server.Shutdown(stopping)
	<-finished
	return e
}

func handler(control Control) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+shedPath, func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, control.LocalShed())
	})
	mux.HandleFunc("PUT "+shedPath, func(w http.ResponseWriter, r *http.Request) {
		var request ShedRequest
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximumBodyBytes)).Decode(&request); e != nil {
			reply(w, http.StatusBadRequest, failure{Code: "VALIDATION_FAILED"})
			return
		}
		bounded, cancel := context.WithTimeout(r.Context(), StoreBudget)
		defer cancel()
		status, e := control.SetLocalShed(bounded, request)
		if e != nil {
			reply(w, http.StatusBadRequest, failure{Code: e.Error()})
			return
		}
		reply(w, http.StatusOK, status)
	})
	return mux
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The peer is the only reader, and a failed write means it left.
	_ = json.NewEncoder(w).Encode(body)
}

// HeldPath is where a level waits on the host, beside the socket at path. The file outlives the
// control process, which stops when it loses its database (ADR 0045), and goes away once the level
// is stored and audited.
func HeldPath(path string) string { return filepath.Join(filepath.Dir(path), heldName) }

// EncodeHeld is the content of a held level.
func EncodeHeld(r ShedRequest) ([]byte, error) { return json.Marshal(r) }

// ReadHeld returns the level waiting at path, if any.
func ReadHeld(path string) (ShedRequest, bool, error) {
	raw, e := os.ReadFile(path)
	if errors.Is(e, fs.ErrNotExist) {
		return ShedRequest{}, false, nil
	}
	if e != nil {
		return ShedRequest{}, false, e
	}
	var r ShedRequest
	if e = json.Unmarshal(raw, &r); e != nil {
		return ShedRequest{}, false, e
	}
	if !r.Valid() {
		return ShedRequest{}, false, errors.New("VALIDATION_FAILED")
	}
	return r, true, nil
}

// WriteHeld atomically replaces the level waiting at path.
func WriteHeld(path string, r ShedRequest) error {
	raw, e := EncodeHeld(r)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".held-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(raw)
	if e = errors.Join(e, f.Sync(), f.Close()); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

// CurrentShed asks the control process listening on path for its level.
func CurrentShed(ctx context.Context, path string) (ShedStatus, error) {
	return exchange(ctx, path, http.MethodGet, nil)
}

// SetShed tells the control process listening on path to change its level.
func SetShed(ctx context.Context, path string, request ShedRequest) (ShedStatus, error) {
	body, e := json.Marshal(request)
	if e != nil {
		return ShedStatus{}, errors.New("VALIDATION_FAILED")
	}
	return exchange(ctx, path, http.MethodPut, body)
}

func exchange(ctx context.Context, path, method string, body []byte) (ShedStatus, error) {
	bounded, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	// The host part is never dialed: the transport below knows only the socket.
	request, e := http.NewRequestWithContext(bounded, method, "http://control"+shedPath, bytes.NewReader(body))
	if e != nil {
		return ShedStatus{}, errors.New(ReasonUnavailable)
	}
	client := http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}}
	response, e := client.Do(request)
	if e != nil {
		return ShedStatus{}, errors.New(ReasonUnavailable)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, maximumBodyBytes))
	if e != nil {
		return ShedStatus{}, errors.New(ReasonInvalidReply)
	}
	if response.StatusCode != http.StatusOK {
		var refused failure
		if json.Unmarshal(raw, &refused) != nil || refused.Code == "" {
			return ShedStatus{}, errors.New(ReasonInvalidReply)
		}
		return ShedStatus{}, errors.New(refused.Code)
	}
	var status ShedStatus
	if json.Unmarshal(raw, &status) != nil || status.Level == "" {
		return ShedStatus{}, errors.New(ReasonInvalidReply)
	}
	return status, nil
}
