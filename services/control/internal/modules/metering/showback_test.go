package metering

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testRateCard = `{
 "currency":"XTS",
 "versions":[
  {"id":"next","effective_at":"2026-11-09T09:00:00Z","rates":[
   {"meter":"requests","units":3,"amount_micros":2},
   {"meter":"storage_byte_seconds","units":1,"amount_micros":0}
  ]},
  {"id":"first","effective_at":"2026-11-09T08:00:00Z","rates":[
   {"meter":"requests","units":4,"amount_micros":1}
  ]}
 ]
}`

func testCard(t *testing.T, raw string) *RateCard {
	t.Helper()
	card, err := DecodeRateCard([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func TestDecodeRateCardRejectsAmbiguousRates(t *testing.T) {
	for name, raw := range map[string]string{
		"missing explicit price": strings.Replace(testRateCard, `,"amount_micros":0`, "", 1),
		"null price":             strings.Replace(testRateCard, `"amount_micros":0`, `"amount_micros":null`, 1),
		"negative price":         strings.Replace(testRateCard, `"amount_micros":0`, `"amount_micros":-1`, 1),
		"duplicate JSON field":   strings.Replace(testRateCard, `"units":3`, `"units":3,"units":4`, 1),
		"unknown field":          strings.Replace(testRateCard, `"units":3`, `"other_units":3`, 1),
		"unknown meter":          strings.Replace(testRateCard, `"requests"`, `"request_minutes"`, 1),
		"duplicate meter":        strings.Replace(testRateCard, `"storage_byte_seconds"`, `"requests"`, 1),
		"duplicate version":      strings.Replace(testRateCard, `"next"`, `"first"`, 1),
		"duplicate instant":      strings.Replace(testRateCard, "09:00:00Z", "16:00:00+08:00", 1),
		"sub-microsecond time":   strings.Replace(testRateCard, "09:00:00Z", "09:00:00.000000001Z", 1),
		"invalid currency":       strings.Replace(testRateCard, `"XTS"`, `"xts"`, 1),
		"extra JSON value":       testRateCard + " {}",
		"missing rates array":    `{"currency":"XTS","versions":[{"id":"v1","effective_at":"2026-11-09T08:00:00Z"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if card, err := DecodeRateCard([]byte(raw)); card != nil || !errors.Is(err, ErrInvalidRateCard) {
				t.Fatalf("accepted invalid rate card: %v", err)
			}
		})
	}
}

var reportSnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

func testShowback(t *testing.T, pool *pgxpool.Pool, scope Scope, from, to time.Time, card *RateCard) ShowbackReport {
	t.Helper()
	var report ShowbackReport
	if err := pgx.BeginTxFunc(t.Context(), pool, reportSnapshot, func(tx pgx.Tx) error {
		var err error
		report, err = ShowbackWithin(t.Context(), tx, scope, from, to, card)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return report
}

func TestPostgresShowbackRatesOccurrenceAndRejectsCrossScope(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	foreign := addTestScope(t, pool)
	sibling := Scope{TenantID: scope.TenantID, WorkspaceID: uuid.New()}
	if _, err := pool.Exec(t.Context(), "INSERT INTO workspaces VALUES ($1,$2)", sibling.TenantID, sibling.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	card := testCard(t, testRateCard)
	start := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	cutover := start.Add(time.Hour)
	end := cutover.Add(time.Hour)
	before := start.Add(-time.Microsecond)
	event := func(id string, when time.Time, usage Usage) Event {
		return Event{Producer: "fixture/showback", ID: id, OccurredAt: when, Usage: usage}
	}
	appendEvent := func(s Scope, e Event) {
		t.Helper()
		if inserted, err := writeEvent(t.Context(), pool, s, e); err != nil || !inserted {
			t.Fatalf("append fixture: %v, %v", inserted, err)
		}
	}
	appendEvent(scope, event("cutover", cutover, Usage{Requests: 4, Tokens: 7, StorageByteSeconds: 11}))
	appendEvent(scope, event("before-first", before, Usage{Requests: 1}))
	appendEvent(scope, event("first", start, Usage{Requests: 1}))
	old := event("late-before-cutover", cutover.Add(-time.Microsecond), Usage{Requests: 1})
	appendEvent(scope, old)
	if inserted, err := writeEvent(t.Context(), pool, scope, old); err != nil || inserted {
		t.Fatalf("replay appended usage: %v, %v", inserted, err)
	}
	appendEvent(scope, event("excluded-end", end, Usage{Requests: 900}))
	appendEvent(foreign, event("foreign", cutover, Usage{Requests: 800}))
	appendEvent(sibling, event("sibling", cutover, Usage{Requests: 700}))

	want := ShowbackReport{Currency: "XTS", From: before, To: end, KnownAmountMicros: "4", FullyRated: false, Lines: []ShowbackLine{
		{Meter: Requests, Quantity: "1"},
		{Meter: Requests, RateVersion: "first", Quantity: "2", Rate: &MeterRate{Units: 4, AmountMicros: 1}, AmountMicros: "1"},
		{Meter: Tokens, RateVersion: "next", Quantity: "7"},
		{Meter: StorageByteSeconds, RateVersion: "next", Quantity: "11", Rate: &MeterRate{Units: 1}, AmountMicros: "0"},
		{Meter: Requests, RateVersion: "next", Quantity: "4", Rate: &MeterRate{Units: 3, AmountMicros: 2}, AmountMicros: "3"},
	}}
	for range 2 {
		if got := testShowback(t, pool, scope, before, end, card); !reflect.DeepEqual(got, want) {
			t.Fatalf("showback got %+v, want %+v", got, want)
		}
	}
	oldPeriod := testShowback(t, pool, scope, start, cutover, card)
	if !oldPeriod.FullyRated || oldPeriod.KnownAmountMicros != "1" ||
		!reflect.DeepEqual(oldPeriod.Lines, want.Lines[1:2]) {
		t.Fatalf("old rate changed at cutover: %+v", oldPeriod)
	}
	mismatched := Scope{TenantID: foreign.TenantID, WorkspaceID: scope.WorkspaceID}
	if got := testShowback(t, pool, mismatched, before, end, card); len(got.Lines) != 0 || got.KnownAmountMicros != "0" {
		t.Fatalf("cross-tenant projection exposed usage: %+v", got)
	}

	if err := pgx.BeginTxFunc(t.Context(), pool, reportSnapshot, func(tx pgx.Tx) error {
		first, err := ShowbackWithin(t.Context(), tx, scope, before, end, card)
		if err != nil {
			return err
		}
		appendEvent(scope, event("new-late-delta", start, Usage{Requests: 4}))
		again, err := ShowbackWithin(t.Context(), tx, scope, before, end, card)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatal("committed ingestion changed an existing report snapshot")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fresh := testShowback(t, pool, scope, before, end, card)
	if len(fresh.Lines) != len(want.Lines) || fresh.KnownAmountMicros != "5" || fresh.Lines[1].Quantity != "6" || fresh.Lines[1].AmountMicros != "2" {
		t.Fatalf("new snapshot did not rate the late delta at its original version: %+v", fresh)
	}
}

func TestPostgresShowbackKeepsLargeAmountsExact(t *testing.T) {
	pool := newTestLedger(t)
	scope := addTestScope(t, pool)
	card := testCard(t, `{"currency":"XTS","versions":[{"id":"v1","effective_at":"2026-11-09T08:00:00Z",
 "rates":[{"meter":"gpu_microseconds","units":3,"amount_micros":9223372036854775807}]}]}`)
	at := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	for _, id := range []string{"first", "second"} {
		e := Event{Producer: "fixture/large", ID: id, OccurredAt: at, Usage: Usage{GPUMicroseconds: math.MaxInt64}}
		if inserted, err := writeEvent(t.Context(), pool, scope, e); err != nil || !inserted {
			t.Fatalf("append large delta: %v, %v", inserted, err)
		}
	}
	got := testShowback(t, pool, scope, at, at.Add(time.Hour), card)
	const quantity = "18446744073709551614"
	const amount = "56713727820156410564931271856155000833"
	if !got.FullyRated || len(got.Lines) != 1 || got.Lines[0].Quantity != quantity ||
		got.Lines[0].AmountMicros != amount || got.KnownAmountMicros != amount {
		t.Fatalf("large exact amount: %+v", got)
	}
	err := pgx.BeginFunc(t.Context(), pool, func(tx pgx.Tx) error {
		_, err := ShowbackWithin(t.Context(), tx, scope, at, at.Add(time.Hour), card)
		return err
	})
	if !errors.Is(err, ErrReportSnapshotRequired) {
		t.Fatalf("accepted a changing write snapshot: %v", err)
	}
}
