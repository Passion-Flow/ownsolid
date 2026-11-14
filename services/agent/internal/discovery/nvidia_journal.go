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
	nvidiaXIDMessage        = regexp.MustCompile(`^NVRM: Xid \((?:PCI:)?([0-9A-Fa-f:.]{10,16})\): ([1-9][0-9]{0,5})(?:,|$)`)
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
		message, err := journalMessage(entry.Message)
		if err != nil {
			return nil, err
		}
		if strings.Contains(message, "[SIMULATED]") {
			continue
		}
		match := nvidiaXIDMessage.FindStringSubmatch(message)
		if match == nil {
			continue
		}
		address := canonicalPCIAddress(match[1])
		if address == "" {
			continue
		}
		usec, err := strconv.ParseInt(entry.Time, 10, 64)
		if err != nil || usec <= 0 || len(entry.Cursor) == 0 || len(entry.Cursor) > 512 || seen[entry.Cursor] || len(events) >= maximumNVIDIAXIDs {
			return nil, ErrNVIDIAJournalInvalid
		}
		code, _ := strconv.ParseUint(match[2], 10, 32)
		events = append(events, NVIDIAXID{BootID: bootID, Cursor: entry.Cursor, PCIAddress: address, Code: uint32(code), ObservedAt: time.UnixMicro(usec).UTC()})
		seen[entry.Cursor] = true
	}
	if lines.Err() != nil || limited.N == 0 {
		return nil, ErrNVIDIAJournalInvalid
	}
	return events, nil
}

func journalMessage(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] != '[' {
		var message string
		if err := json.Unmarshal(raw, &message); err != nil {
			return "", ErrNVIDIAJournalInvalid
		}
		return message, nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return "", ErrNVIDIAJournalInvalid
	}
	var repeated bool
	message := make([]byte, 0, len(elements))
	for _, element := range elements {
		switch element[0] {
		case '"', '[', 'n':
			// journalctl uses an array of strings, byte arrays or nulls for repeated values.
			repeated = true
		default:
			value, err := strconv.ParseUint(string(element), 10, 8)
			if err != nil {
				return "", ErrNVIDIAJournalInvalid
			}
			message = append(message, byte(value))
		}
	}
	if repeated && len(message) != 0 {
		return "", ErrNVIDIAJournalInvalid
	}
	return string(message), nil
}

var pciAddress = regexp.MustCompile(`^([0-9a-fA-F]{4}(?:[0-9a-fA-F]{4})?):([0-9a-fA-F]{2}):([01][0-9a-fA-F])(\.[0-7])?$`)

func canonicalPCIAddress(address string) string {
	parts := pciAddress.FindStringSubmatch(address)
	if parts == nil {
		return ""
	}
	if len(parts[1]) == 4 {
		return "0000" + strings.ToLower(address)
	}
	return strings.ToLower(address)
}
