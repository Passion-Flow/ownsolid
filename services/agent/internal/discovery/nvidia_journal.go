package discovery

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maximumNVIDIAJournalBytes = 1024 * 1024
	maximumNVIDIAJournalLine  = 16 * 1024
	maximumNVIDIAXIDs         = 1024
)

var (
	// ErrNVIDIAJournalInvalid means the collector could not safely read a batch.
	ErrNVIDIAJournalInvalid = errors.New("NVIDIA_JOURNAL_INVALID: check the current boot, journal cursor and batch bounds")
	journalBootID           = regexp.MustCompile(`^[0-9a-f]{32}$`)
	nvidiaXIDMessage        = regexp.MustCompile(`^NVRM: Xid \((?:PCI:)?([0-9A-Fa-f]{4}(?:[0-9A-Fa-f]{4})?):([0-9A-Fa-f]{2}):([01][0-9A-Fa-f])(\.[0-7])?\): ([1-9][0-9]{0,5})(?:,|$)`)
)

// NVIDIAXID records a kernel observation. The PCI function is absent when
// the driver omitted it. Cursor identifies the event within the local journal.
type NVIDIAXID struct {
	BootID     string
	Cursor     string
	PCIAddress string
	Code       uint32
	ObservedAt time.Time
}

type nvidiaJournalEntry struct {
	BootID    string          `json:"_BOOT_ID"`
	Cursor    string          `json:"__CURSOR"`
	Time      string          `json:"__REALTIME_TIMESTAMP"`
	Transport string          `json:"_TRANSPORT"`
	Facility  string          `json:"SYSLOG_FACILITY"`
	Message   json.RawMessage `json:"MESSAGE"`
}

// ReadNVIDIAJournal reads JSON lines from the trusted local kernel journal
// for the given boot. Replaying the same input yields the same cursor and time.
func ReadNVIDIAJournal(reader io.Reader, bootID string) ([]NVIDIAXID, error) {
	if !journalBootID.MatchString(bootID) {
		return nil, ErrNVIDIAJournalInvalid
	}
	limited := &io.LimitedReader{R: reader, N: maximumNVIDIAJournalBytes + 1}
	lines := bufio.NewScanner(limited)
	lines.Buffer(make([]byte, 4096), maximumNVIDIAJournalLine)
	var events []NVIDIAXID
	seen := map[string]bool{}
	for lines.Scan() {
		var entry nvidiaJournalEntry
		if err := json.Unmarshal(lines.Bytes(), &entry); err != nil {
			return nil, ErrNVIDIAJournalInvalid
		}
		if entry.Transport != "kernel" || entry.Facility != "0" || entry.BootID != bootID {
			continue
		}
		var message string
		if err := json.Unmarshal(entry.Message, &message); err != nil {
			return nil, ErrNVIDIAJournalInvalid
		}
		match := nvidiaXIDMessage.FindStringSubmatch(message)
		if match == nil {
			continue
		}
		usec, err := strconv.ParseInt(entry.Time, 10, 64)
		if err != nil || usec <= 0 || len(entry.Cursor) == 0 || len(entry.Cursor) > 512 || seen[entry.Cursor] || len(events) >= maximumNVIDIAXIDs {
			return nil, ErrNVIDIAJournalInvalid
		}
		domain := match[1]
		if len(domain) == 4 {
			domain = "0000" + domain
		}
		address := strings.ToLower(domain + ":" + match[2] + ":" + match[3] + match[4])
		code, _ := strconv.ParseUint(match[5], 10, 32)
		events = append(events, NVIDIAXID{BootID: bootID, Cursor: entry.Cursor, PCIAddress: address, Code: uint32(code), ObservedAt: time.UnixMicro(usec).UTC()})
		seen[entry.Cursor] = true
	}
	if lines.Err() != nil || limited.N == 0 {
		return nil, ErrNVIDIAJournalInvalid
	}
	return events, nil
}
