package discovery

import (
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
	return fmt.Sprintf(`{"_BOOT_ID":%q,"__CURSOR":%q,"__REALTIME_TIMESTAMP":%q,"_TRANSPORT":%q,"SYSLOG_FACILITY":%q,"MESSAGE":%q}`, boot, cursor, usec, transport, facility, message) + "\n"
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

func TestReadNVIDIAJournalFailsAtomically(t *testing.T) {
	const msg = "NVRM: Xid (0000:ab:00): 48"
	valid := xidJournalLine(currentBoot, "first", "1", "kernel", "0", msg)
	large := strings.Repeat("{\"MESSAGE\":\"other\"}\n", maximumNVIDIAJournalBytes/10)
	for _, test := range []struct{ name, body string }{
		{"duplicate cursor", valid},
		{"bad timestamp", xidJournalLine(currentBoot, "next", "0", "kernel", "0", msg)},
		{"missing cursor", xidJournalLine(currentBoot, "", "2", "kernel", "0", msg)},
		{"malformed JSON", "{\"MESSAGE\":"},
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
