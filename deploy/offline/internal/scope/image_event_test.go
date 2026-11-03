package scope

import (
	"strings"
	"testing"
)

func TestImageAdmissionBoundsCompleteJournalEnvelope(t *testing.T) {
	record := ImageRecord{AssetID: "current-control", Path: "images/current-control.tar", SourceSHA256: strings.Repeat("a", 64), SourceBytes: 426140160, ManifestDigest: "sha256:" + strings.Repeat("b", 64), ConfigDigest: "sha256:" + strings.Repeat("c", 64), DiffIDs: []string{}, ExpandedBytes: 1707393300, TransportSHA256: strings.Repeat("d", 64), TransportBytes: 426130000, Mode: "load"}
	for i := 0; i < 20; i++ {
		record.DiffIDs = append(record.DiffIDs, "sha256:"+strings.Repeat("e", 64))
	}
	if !validImage(&record) || !imageEventFits(record) {
		t.Fatal("actual20-layer image envelope unnecessarily refused")
	}
	// The old partial-envelope estimate wrongly refused this real-sized record.
	if len(encode(Event{Image: &record}))+2048 <= maximumEventBytes {
		t.Fatal("regression fixture does not cover the original refusal")
	}
	for len(record.DiffIDs) < 128 {
		record.DiffIDs = append(record.DiffIDs, "sha256:"+strings.Repeat("e", 64))
	}
	if !validImage(&record) || imageEventFits(record) {
		t.Fatal("oversized128-layer journal event admitted")
	}
	record.DiffIDs = []string{}
	record.Path = strings.Repeat("a", maximumEventBytes)
	if imageEventFits(record) {
		t.Fatal("oversized path admitted")
	}
}
