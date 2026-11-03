package components

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type maintenanceService interface {
	serviceManager
	Restart(context.Context) error
	ValidateUnit(context.Context, string) error
	Journal(context.Context, time.Time) (wire.ComponentLogs, error)
}

func (s systemd) Restart(ctx context.Context) error {
	_, e := s.command(ctx, "restart", unitName)
	return e
}
func (s systemd) ValidateUnit(ctx context.Context, path string) error {
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(unitPath) + `\.staging-[a-f0-9]{32}\.service$`)
	if !pattern.MatchString(path) {
		return code("COMPONENT_STATE_INVALID")
	}
	if _, e := hashFile(path); e != nil {
		return e
	}
	_, e := maintenanceOutput(ctx, "/usr/bin/systemd-analyze", []string{"verify", path}, 16384, settings.Resolve(s.runtime))
	if e != nil {
		return code("COMPONENT_SERVICE_FAILED")
	}
	return nil
}

type limitedOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedOutput) Write(value []byte) (int, error) {
	n := len(value)
	if b.Len()+n > b.limit {
		b.exceeded = true
		return n, nil
	}
	b.Buffer.Write(value)
	return n, nil
}
func maintenanceOutput(ctx context.Context, program string, args []string, maximum int, policies ...settings.Policy) ([]byte, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	info, e := os.Lstat(program)
	if e != nil || !info.Mode().IsRegular() || !rootOwned(info) || info.Mode().Perm()&0022 != 0 {
		return nil, code("COMPONENT_LOGS_UNAVAILABLE")
	}
	bounded, cancel := context.WithTimeout(ctx, settings.Seconds(policy.MaintenanceSeconds))
	defer cancel()
	cmd := exec.CommandContext(bounded, program, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output := &limitedOutput{limit: maximum}
	diagnostic := &limitedOutput{limit: 4096}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	if e = cmd.Run(); e != nil {
		return nil, code("COMPONENT_LOGS_UNAVAILABLE")
	}
	if output.exceeded || diagnostic.exceeded {
		return nil, code("COMPONENT_LOGS_TOO_LARGE")
	}
	return output.Bytes(), nil
}
func (s systemd) Journal(ctx context.Context, since time.Time) (wire.ComponentLogs, error) {
	active, e := s.command(ctx, "show", "systemd-journald.service", "--property=ActiveState", "--value")
	if e != nil || active != "active" {
		return wire.ComponentLogs{}, code("COMPONENT_LOGS_UNAVAILABLE")
	}
	if since.IsZero() || since.Year() < 2020 || since.After(time.Now().Add(time.Minute)) {
		return wire.ComponentLogs{}, code("COMPONENT_LOGS_INVALID")
	}
	body, e := maintenanceOutput(ctx, "/usr/bin/journalctl", []string{"--system", "--no-pager", "--quiet", "--unit=" + unitName, "--since=@" + strconv.FormatInt(since.Unix(), 10), "--lines=8", "--output=json", "--output-fields=__REALTIME_TIMESTAMP,PRIORITY,MESSAGE"}, 65536, settings.Resolve(s.runtime))
	if e != nil {
		return wire.ComponentLogs{}, e
	}
	return parseJournal(body, since, time.Now().UTC())
}
func sanitizeMessage(value string) (string, bool, bool) {
	changed := false
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			r = ' '
			changed = true
		}
		builder.WriteRune(r)
	}
	message := builder.String()
	truncated := len(message) > 512
	if truncated {
		message = message[:512]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return message, changed, truncated
}
func parseJournal(body []byte, since, observed time.Time) (wire.ComponentLogs, error) {
	out := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: observed, Since: since, Entries: []wire.ComponentLogEntry{}}
	if len(body) > 65536 || !utf8.Valid(body) {
		return out, code("COMPONENT_LOGS_TOO_LARGE")
	}
	for _, line := range bytes.Split(body, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(out.Entries) >= 8 {
			return out, code("COMPONENT_LOGS_TOO_LARGE")
		}
		var record struct {
			Timestamp string  `json:"__REALTIME_TIMESTAMP"`
			Priority  string  `json:"PRIORITY"`
			Message   *string `json:"MESSAGE"`
		}
		if json.Unmarshal(line, &record) != nil || record.Message == nil {
			return out, code("COMPONENT_LOGS_INVALID")
		}
		micros, e := strconv.ParseInt(record.Timestamp, 10, 64)
		if e != nil {
			return out, code("COMPONENT_LOGS_INVALID")
		}
		priority, e := strconv.Atoi(record.Priority)
		if e != nil || priority < 0 || priority > 7 {
			return out, code("COMPONENT_LOGS_INVALID")
		}
		message, sanitized, truncated := sanitizeMessage(*record.Message)
		out.Truncated = out.Truncated || truncated
		out.Entries = append(out.Entries, wire.ComponentLogEntry{Timestamp: time.UnixMicro(micros).UTC(), Priority: priority, Message: message, Sanitized: sanitized})
	}
	// HTML-safe JSON escaping can expand hostile text; bound the encoded machine response too.
	for {
		encoded, e := json.Marshal(out)
		if e != nil {
			return out, code("COMPONENT_LOGS_INVALID")
		}
		if len(encoded) <= 12288 {
			break
		}
		out.Truncated = true
		last := len(out.Entries) - 1
		if last < 0 {
			return out, code("COMPONENT_LOGS_TOO_LARGE")
		}
		message := out.Entries[last].Message
		if len(message) == 0 {
			out.Entries = out.Entries[:last]
			continue
		}
		message = message[:len(message)/2]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		out.Entries[last].Message = message
	}
	if e := out.Validate(); e != nil {
		return out, code("COMPONENT_LOGS_INVALID")
	}
	return out, nil
}
