// Package telemetrystore reads and writes the dedicated telemetry and log stores.
package telemetrystore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// maximumResponseBytes bounds one query response. Bodies are at most 16 KiB and
	// callers ask for a handful of records, so this leaves room for JSON escaping.
	maximumResponseBytes = 1 << 20
	insertPath           = "/insert/jsonline"
	queryPath            = "/select/logsql/query"
	// streamFields group records by what a query selects on, which the store indexes.
	streamFields = "tenant_id,workspace_id,node_id,component"
)

// ErrUnavailable means the store did not answer. Callers report it as retryable.
var ErrUnavailable = errors.New("telemetry store unavailable")

// Scope selects one component's records on one node. Its values come from the server's
// authorized context, never from client input.
type Scope struct {
	TenantID, WorkspaceID, NodeID, Component string
}

func (s Scope) valid() bool {
	return s.TenantID != "" && s.WorkspaceID != "" && s.NodeID != "" && s.Component != ""
}

// Record is one log record. Removed marks a tombstone: the store has no delete, so a
// reader treats a tombstone as the end of what came before it.
type Record struct {
	Scope
	Time    time.Time
	TaskID  string
	Removed bool
	Body    json.RawMessage
}

// wireRecord is the store's JSON line. _msg must be a string, so the body is nested.
type wireRecord struct {
	Time        time.Time `json:"_time"`
	Message     string    `json:"_msg"`
	TenantID    string    `json:"tenant_id"`
	WorkspaceID string    `json:"workspace_id"`
	NodeID      string    `json:"node_id"`
	Component   string    `json:"component"`
	TaskID      string    `json:"task_id,omitempty"`
	Removed     string    `json:"removed,omitempty"`
}

const (
	removedMarker  = "true"
	removedMessage = "removed"
)

// Client talks to one telemetry store over HTTP.
type Client struct {
	base *url.URL
	http *http.Client
}

// NewClient returns a client for the store at address, an http URL without a path.
func NewClient(address string, timeout time.Duration) (*Client, error) {
	base, e := url.Parse(address)
	if e != nil || base.Scheme != "http" || base.Host == "" || base.Path != "" || base.RawQuery != "" || base.User != nil {
		return nil, fmt.Errorf("telemetry store address %q must be an http URL without a path", address)
	}
	if timeout <= 0 {
		return nil, errors.New("telemetry store timeout must be positive")
	}
	// No redirects: the address is fixed configuration, so a redirect is never expected.
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{base: base, http: client}, nil
}

func (c *Client) endpoint(path string, query url.Values) string {
	u := *c.base
	u.Path = path
	u.RawQuery = query.Encode()
	return u.String()
}

// Insert writes one record.
func (c *Client) Insert(ctx context.Context, r Record) error {
	if !r.valid() || r.Time.IsZero() {
		return errors.New("telemetry record is incomplete")
	}
	line := wireRecord{Time: r.Time.UTC(), Message: string(r.Body), TenantID: r.TenantID, WorkspaceID: r.WorkspaceID, NodeID: r.NodeID, Component: r.Component, TaskID: r.TaskID}
	if r.Removed {
		line.Removed, line.Message = removedMarker, removedMessage
	}
	raw, e := json.Marshal(line)
	if e != nil {
		return fmt.Errorf("encode telemetry record: %w", e)
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(insertPath, url.Values{"_stream_fields": {streamFields}}), bytes.NewReader(append(raw, '\n')))
	if e != nil {
		return fmt.Errorf("build telemetry request: %w", e)
	}
	// A form content type makes the store read the body as a form: it answers 200 and
	// stores nothing, so this header decides whether the record is kept.
	request.Header.Set("Content-Type", "application/stream+json")
	return c.exchange(request, nil)
}

// Recent returns up to limit records in scope, newest first.
func (c *Client) Recent(ctx context.Context, s Scope, limit int) ([]Record, error) {
	if !s.valid() || limit < 1 {
		return nil, errors.New("telemetry query is incomplete")
	}
	form := url.Values{"query": {recentQuery(s, limit)}}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(queryPath, nil), strings.NewReader(form.Encode()))
	if e != nil {
		return nil, fmt.Errorf("build telemetry request: %w", e)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var records []Record
	e = c.exchange(request, func(body io.Reader) error {
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64<<10), maximumResponseBytes)
		for scanner.Scan() {
			var line wireRecord
			if e := json.Unmarshal(scanner.Bytes(), &line); e != nil {
				return fmt.Errorf("decode telemetry record: %w", e)
			}
			// The store echoes the scope; a record outside it must never reach the caller.
			if line.TenantID != s.TenantID || line.WorkspaceID != s.WorkspaceID || line.NodeID != s.NodeID || line.Component != s.Component {
				return errors.New("telemetry store returned a record outside the requested scope")
			}
			record := Record{Scope: s, Time: line.Time, TaskID: line.TaskID, Removed: line.Removed == removedMarker}
			if !record.Removed {
				record.Body = json.RawMessage(line.Message)
			}
			records = append(records, record)
		}
		return scanner.Err()
	})
	if e != nil {
		return nil, e
	}
	return records, nil
}

// recentQuery builds the LogsQL selector. strconv.Quote keeps every value a single
// quoted string, so a value cannot change the query.
func recentQuery(s Scope, limit int) string {
	return fmt.Sprintf(`{tenant_id=%s,workspace_id=%s,node_id=%s,component=%s} | sort by (_time desc) limit %d`,
		strconv.Quote(s.TenantID), strconv.Quote(s.WorkspaceID), strconv.Quote(s.NodeID), strconv.Quote(s.Component), limit)
}

// exchange sends a request and hands a successful body to read, when there is one.
func (c *Client) exchange(request *http.Request, read func(io.Reader) error) error {
	response, e := c.http.Do(request)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, e)
	}
	defer response.Body.Close()
	body := io.LimitReader(response.Body, maximumResponseBytes)
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	if read == nil {
		_, e = io.Copy(io.Discard, body)
		return e
	}
	return read(body)
}
