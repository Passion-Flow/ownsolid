//go:build linux || darwin

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

const maxSIEMWindow = 4096

var (
	canonicalUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	auditCode     = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,95}$`)
)

type siemWindow struct {
	from, through  int64
	previous, last string
}

func (w siemWindow) valid() bool {
	_, previousOK := hashBytes(w.previous)
	_, lastOK := hashBytes(w.last)
	return w.from >= 0 && w.through > w.from && w.through-w.from <= maxSIEMWindow &&
		previousOK && lastOK && (w.from == 0) == (w.previous == zeroHash) && w.last != zeroHash
}

type siemDelivery struct {
	hash  string
	frame []byte
}

type siemReport struct {
	Position  int64  `json:"cursor_position"`
	Sent      int64  `json:"sent"`
	TotalSent int64  `json:"total_sent"`
	Dropped   int64  `json:"dropped"`
	Error     string `json:"error,omitempty"`
}

func validSIEMUUID(value string) bool {
	return canonicalUUID.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func siemFrame(row record) ([]byte, error) {
	event, action, outcome := row.value("id"), row.value("action"), row.value("outcome")
	if !validSIEMUUID(event) || !auditCode.MatchString(action) || !auditCode.MatchString(outcome) {
		return nil, errors.New("AUDIT_SIEM_EVENT_INVALID")
	}
	body, err := json.Marshal(struct {
		Format   string `json:"format"`
		EventID  string `json:"event_id"`
		TenantID string `json:"tenant_id"`
		Action   string `json:"action"`
		Outcome  string `json:"outcome"`
	}{
		Format: "ownsolid.audit.siem.v1", EventID: event, TenantID: row.value("tenant_id"),
		Action: action, Outcome: outcome,
	})
	if err != nil {
		return nil, err
	}
	// Local0 and informational severity. The timestamp is validated by the verifier.
	message := "<134>1 " + row.value("occurred_at") + " - ownsolid - AUDIT - " + string(body)
	return []byte(strconv.Itoa(len(message)) + " " + message), nil
}

func verifiedSIEMWindow(ctx context.Context, input io.Reader, window siemWindow, tenant string) ([]siemDelivery, error) {
	delivery := make([]siemDelivery, 0, window.through-window.from)
	var eventError error
	checked := verifyWindow(input, window.previous, window.last, false, func(row record, position int64) string {
		if ctx.Err() != nil {
			eventError = errors.New("AUDIT_SIEM_INTERRUPTED")
			return "delivery interrupted"
		}
		if position != window.from+int64(len(delivery))+1 || position > window.through {
			return "unexpected window position"
		}
		entry := siemDelivery{hash: row.value("hash")}
		if row.value("tenant_id") == tenant {
			entry.frame, eventError = siemFrame(row)
			if eventError != nil {
				return "invalid delivery fields"
			}
		}
		delivery = append(delivery, entry)
		return ""
	})
	if eventError != nil {
		return nil, eventError
	}
	if !checked.Valid || checked.Records != window.through-window.from {
		return nil, errors.New("AUDIT_SIEM_WINDOW_INVALID")
	}
	return delivery, nil
}

func forwardSIEM(ctx context.Context, input io.Reader, window siemWindow, grant siemGrant,
	store *siemCursorStore, send func(context.Context, []byte) error,
) siemReport {
	start := store.state
	result := siemReport{Position: start.Position, TotalSent: start.Sent}
	records, err := verifiedSIEMWindow(ctx, input, window, grant.TenantID)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if start.Position < window.from || start.Position > window.through {
		result.Error = "AUDIT_SIEM_CURSOR_OUTSIDE_WINDOW"
		return result
	}
	offset := start.Position - window.from
	expected := window.previous
	if offset > 0 {
		expected = records[offset-1].hash
	}
	if start.Hash != expected {
		result.Error = "AUDIT_SIEM_CURSOR_MISMATCH"
		return result
	}
	if !store.exists {
		if err := store.save(start); err != nil {
			result.Error = err.Error()
			return result
		}
	}

	next := start
	for _, entry := range records[offset:] {
		if ctx.Err() != nil {
			result.Error = "AUDIT_SIEM_INTERRUPTED"
			break
		}
		if entry.frame != nil {
			if err := send(ctx, entry.frame); err != nil {
				result.Error = err.Error()
				break
			}
			next.Sent++
		}
		next.Position++
		next.Hash = entry.hash
	}
	if next != start {
		if err := store.save(next); err != nil {
			result.Error = err.Error()
			return result
		}
		result.Position, result.Sent, result.TotalSent = next.Position, next.Sent-start.Sent, next.Sent
	}
	return result
}

func runSIEM(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("forward-syslog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var window siemWindow
	flags.Int64Var(&window.from, "previous-position", -1, "trusted position immediately before this export")
	flags.Int64Var(&window.through, "last-position", -1, "trusted final position of this export")
	flags.StringVar(&window.previous, "previous-hash", "", "trusted hash immediately before this export")
	flags.StringVar(&window.last, "expected-last-hash", "", "trusted final hash of this export")
	inputPath := flags.String("file", "", "protected JSONL export file")
	grantPath := flags.String("grant", "", "protected local SIEM grant file")
	timeout := flags.Duration("timeout", 5*time.Second, "timeout per delivery attempt")
	attempts := flags.Int("attempts", 3, "maximum attempts for a frame")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *inputPath == "" || *grantPath == "" || !window.valid() ||
		*timeout < 100*time.Millisecond || *timeout > 30*time.Second || *attempts < 1 || *attempts > 5 {
		fmt.Fprintln(stderr, "AUDIT_SIEM_ARGUMENT_INVALID")
		return 2
	}
	grant, tlsConfig, err := loadSIEMGrant(*grantPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	input, err := openSIEMFile(*inputPath, true)
	if err != nil {
		return writeSIEMReport(stdout, siemReport{Error: "AUDIT_SIEM_EXPORT_UNAVAILABLE"})
	}
	defer input.Close()
	store, err := openSIEMCursor(grant.CursorDirectory, grant.binding(), window)
	if err != nil {
		return writeSIEMReport(stdout, siemReport{Error: err.Error()})
	}
	defer store.close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	sender := siemSender{address: grant.Endpoint, config: tlsConfig, timeout: *timeout, attempts: *attempts}
	defer sender.close()
	return writeSIEMReport(stdout, forwardSIEM(ctx, input, window, grant, store, sender.send))
}

func writeSIEMReport(output io.Writer, result siemReport) int {
	if json.NewEncoder(output).Encode(result) != nil || result.Error != "" {
		return 1
	}
	return 0
}
