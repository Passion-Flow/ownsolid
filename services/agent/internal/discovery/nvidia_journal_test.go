package discovery

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

const currentBoot = "0123456789abcdef0123456789abcdef"
const staleBoot = "123456789abcdef0123456789abcdef0"

func xidJournalLine(boot, cursor, usec, transport, facility, message string) string {
	messageJSON, _ := json.Marshal(message)
	return xidJournalRawMessage(boot, cursor, usec, transport, facility, string(messageJSON))
}

func xidJournalRawMessage(boot, cursor, usec, transport, facility, messageJSON string) string {
	return fmt.Sprintf("{\"_BOOT_ID\":%q,\"__CURSOR\":%q,\"__REALTIME_TIMESTAMP\":%q,\"_TRANSPORT\":%q,\"SYSLOG_FACILITY\":%q,\"MESSAGE\":%s}\n", boot, cursor, usec, transport, facility, messageJSON)
}

func TestReadNVIDIAJournalChecksProvenanceAndIdentity(t *testing.T) {
	const usec = "1794160800000000"
	const drop = "NVRM: Xid (PCI:0000:AB:00): 79, workload details"
	const remap = "NVRM: Xid (00000001:ab:01.3): 63"
	body := xidJournalLine(currentBoot, "valid-drop", usec, "kernel", "0", drop) +
		xidJournalLine(currentBoot, "userspace-kmsg", usec, "kernel", "1", drop) +
		xidJournalLine(currentBoot, "syslog", usec, "syslog", "0", drop) +
		xidJournalLine(staleBoot, "prior-boot", usec, "kernel", "0", drop) +
		xidJournalLine(currentBoot, "unanchored", usec, "kernel", "0", "workload: "+drop) +
		xidJournalLine(currentBoot, "suffix", usec, "kernel", "0", "NVRM: Xid (0000:ab:00): 79x") +
		xidJournalLine(currentBoot, "bad-device", usec, "kernel", "0", "NVRM: Xid (0000:ab:ff): 79") +
		xidJournalLine(currentBoot, "valid-remap", usec, "kernel", "0", remap)
	at := time.UnixMicro(1794160800000000).UTC()
	want := []NVIDIAXID{
		{BootID: currentBoot, Cursor: "valid-drop", PCIAddress: "00000000:ab:00", Code: 79, ObservedAt: at},
		{BootID: currentBoot, Cursor: "valid-remap", PCIAddress: "00000001:ab:01.3", Code: 63, ObservedAt: at},
	}
	for range 2 {
		got, err := ReadNVIDIAJournal(strings.NewReader(body), currentBoot)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ReadNVIDIAJournal = %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestReadNVIDIAJournalHandlesAlternateMessageEncodings(t *testing.T) {
	line := func(cursor, message string) string {
		return xidJournalRawMessage(currentBoot, cursor, "2", "kernel", "0", message)
	}
	byteArray := func(message string) string {
		values := make([]int, len(message))
		for i := range len(message) {
			values[i] = int(message[i])
		}
		raw, _ := json.Marshal(values)
		return string(raw)
	}
	body := line("first", "\"NVRM: Xid (0000:ab:00): 79\"") +
		line("unrelated binary", "[255,0,128]") +
		line("omitted", "null") +
		line("repeated", "[\"NVRM: Xid (0000:ab:00): 79\",null,[255]]") +
		line("binary xid", byteArray("NVRM: Xid (00000001:AB:00.2): 48, detail \xff")) +
		line("simulated ecc", byteArray("NVRM: Xid (0000:ab:00): 48, detail \xff [SIMULATED]")) +
		line("last", "\"NVRM: Xid (0000:ab:00): 63, detail\"")
	at := time.UnixMicro(2).UTC()
	want := []NVIDIAXID{
		{BootID: currentBoot, Cursor: "first", PCIAddress: "00000000:ab:00", Code: 79, ObservedAt: at},
		{BootID: currentBoot, Cursor: "binary xid", PCIAddress: "00000001:ab:00.2", Code: 48, ObservedAt: at},
		{BootID: currentBoot, Cursor: "last", PCIAddress: "00000000:ab:00", Code: 63, ObservedAt: at},
	}
	got, err := ReadNVIDIAJournal(strings.NewReader(body), currentBoot)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadNVIDIAJournal = %+v, %v; want %+v", got, err, want)
	}
}

func TestReadNVIDIAJournalExcludesSimulatedXIDs(t *testing.T) {
	const drop = "NVRM: Xid (0000:ab:00): 79"
	const ecc = "NVRM: Xid (0000:ab:00): 48"
	line := func(cursor, message string) string {
		return xidJournalLine(currentBoot, cursor, "2", "kernel", "0", message)
	}
	body := line("simulated drop", drop+", [SIMULATED] device lost") +
		line("real drop", drop+", PCIe failure") +
		line("simulated ecc", ecc+", detail [SIMULATED]") +
		line("real ecc", ecc+", detail")
	got, err := ReadNVIDIAJournal(strings.NewReader(body), currentBoot)
	if err != nil || len(got) != 2 || got[0].Cursor != "real drop" || got[0].Code != 79 ||
		got[1].Cursor != "real ecc" || got[1].Code != 48 {
		t.Fatalf("ReadNVIDIAJournal retained simulated observations or lost genuine ones: %+v, %v", got, err)
	}
}

func TestReadNVIDIAJournalFailsAtomically(t *testing.T) {
	const msg = "NVRM: Xid (0000:ab:00): 48"
	valid := xidJournalLine(currentBoot, "first", "1", "kernel", "0", msg)
	large := strings.Repeat("{\"MESSAGE\":\"other\"}\n", maximumNVIDIAJournalBytes/10)
	for _, test := range []struct{ name, body string }{
		{"duplicate cursor", valid},
		{"bad timestamp", xidJournalLine(currentBoot, "next", "0", "kernel", "0", msg)},
		{"missing cursor", xidJournalLine(currentBoot, "", "2", "kernel", "0", msg)},
		{"malformed JSON", "{\"MESSAGE\":"},
		{"invalid message byte", xidJournalRawMessage(currentBoot, "next", "2", "kernel", "0", "[256]")},
		{"mixed message encodings", xidJournalRawMessage(currentBoot, "next", "2", "kernel", "0", "[78,\"NVRM\"]")},
		{"oversized entry", strings.Repeat("x", maximumNVIDIAJournalLine)},
		{"oversized batch", large},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := ReadNVIDIAJournal(strings.NewReader(valid+test.body), currentBoot)
			if got != nil || err != ErrNVIDIAJournalInvalid {
				t.Fatalf("ReadNVIDIAJournal = %+v, %v; want no partial batch", got, err)
			}
		})
	}
	if got, err := ReadNVIDIAJournal(strings.NewReader(valid), ""); got != nil || err != ErrNVIDIAJournalInvalid {
		t.Fatalf("invalid boot accepted: %+v, %v", got, err)
	}
	if got, err := ReadNVIDIAJournal(io.LimitReader(strings.NewReader(valid), 0), currentBoot); len(got) != 0 || err != nil {
		t.Fatalf("empty journal: %+v, %v", got, err)
	}
}
