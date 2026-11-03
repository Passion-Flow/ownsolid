package v1_test

import (
	"encoding/json"
	"strings"
	"testing"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func TestPreparationEventsBindExactCompleteEvaluationOrder(t *testing.T) {
	preparation, attempt := "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"
	rows := int64(3)
	for sequence := int64(1); sequence <= 2*rows+3; sequence++ {
		e := wire.Event{Sequence: sequence, PreparationID: preparation, AttemptID: attempt, Kind: "phase"}
		switch sequence {
		case 1:
			e.Phase = "evaluating_base"
		case rows + 2:
			e.Phase = "merging"
		case rows + 3:
			e.Phase = "evaluating_candidate"
		default:
			e.Kind = "evaluation_progress"
			e.Phase = "base"
			completed := sequence - 1
			if sequence > rows+3 {
				e.Phase = "candidate"
				completed = sequence - rows - 3
			}
			e.CompletedRows = &completed
			e.TotalRows = &rows
		}
		raw, _ := json.Marshal(e)
		if _, err := wire.DecodeEvent(raw, preparation, attempt, rows); err != nil {
			t.Fatal("valid actual computation order", sequence, err)
		}
		e.Sequence++
		raw, _ = json.Marshal(e)
		if _, err := wire.DecodeEvent(raw, preparation, attempt, rows); err == nil {
			t.Fatal("shifted evaluation step accepted", sequence)
		}
	}
	valid := `{"sequence":1,"preparation_id":"` + preparation + `","attempt_id":"` + attempt + `","kind":"phase","phase":"evaluating_base"}`
	for _, raw := range []string{strings.Replace(valid, `"sequence":1`, `"sequence":1.0`, 1), strings.Replace(valid, `"sequence":1`, `"sequence":1,"sequence":1`, 1), strings.Replace(valid, `"sequence":1,`, "", 1), strings.Replace(valid, `"phase":"evaluating_base"`, `"phase":"evaluating_base","total_rows":null`, 1), strings.Replace(valid, `"phase":"evaluating_base"`, `"phase":"evaluating_base","host_path":"/private"`, 1), strings.Replace(valid, attempt, preparation, 1)} {
		if _, err := wire.DecodeEvent([]byte(raw), preparation, attempt, rows); err == nil {
			t.Fatal("noncanonical event accepted")
		}
	}
}

func TestPreparationInputRangeRejectsPathsWrongKindAndIncompleteChunk(t *testing.T) {
	asset := dataset.Asset{Path: "heldout.jsonl", SizeBytes: wire.TransferChunkBytes + 7, SHA256: strings.Repeat("a", 64)}
	input := wire.InputFileRequest{LeaseToken: strings.Repeat("a", 43), Kind: "heldout", Asset: asset.Path, Length: wire.TransferChunkBytes}
	if input.ValidateAsset(asset) != nil {
		t.Fatal("first exact range")
	}
	input.Offset = wire.TransferChunkBytes
	input.Length = 7
	if input.ValidateAsset(asset) != nil {
		t.Fatal("last exact range")
	}
	for _, mutate := range []func(*wire.InputFileRequest){func(r *wire.InputFileRequest) { r.Kind = "checkpoint" }, func(r *wire.InputFileRequest) { r.Asset = "../heldout.jsonl" }, func(r *wire.InputFileRequest) { r.Offset++ }, func(r *wire.InputFileRequest) { r.Length-- }, func(r *wire.InputFileRequest) { r.LeaseToken = "" }} {
		r := input
		mutate(&r)
		if r.ValidateAsset(asset) == nil {
			t.Fatal("invalid input range accepted")
		}
	}
}
