package v1

import (
	"strconv"
	"testing"
	"time"
)

func TestSessionConnectionAgeHintBounds(t *testing.T) {
	start := time.Now()
	positive := []string{"1000000000"}
	closing := []string{"2000000000"}
	for name, drain := range map[string][]string{"missing": nil, "duplicate": {"1", "1"}, "zero": {"0"}, "negative": {"-1"}, "plus": {"+1"}, "leading_zero": {"01"}, "space": {" 1"}, "noninteger": {"1.5"}, "overflow": {"999999999999999999999"}, "above_bound": {strconv.FormatInt(int64(MaxSessionConnectionHint)+1, 10)}, "non_ascii": {"\uFF11"}} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseSessionConnectionAge(drain, closing, start); ok {
				t.Fatal("invalid drain hint was accepted")
			}
		})
	}
	for name, closing := range map[string][]string{"missing": nil, "duplicate": {"2", "2"}, "before_drain": {"1"}, "invalid": {"broken"}} {
		t.Run("close_"+name, func(t *testing.T) {
			if _, ok := ParseSessionConnectionAge(positive, closing, start); ok {
				t.Fatal("invalid close hint was accepted")
			}
		})
	}
	if _, ok := ParseSessionConnectionAge(positive, closing, time.Time{}); ok {
		t.Fatal("missing local start was accepted")
	}
	maximum := []string{strconv.FormatInt(int64(MaxSessionConnectionHint), 10)}
	if _, ok := ParseSessionConnectionAge(maximum, maximum, start); !ok {
		t.Fatal("maximum inclusive bound was refused")
	}
	age, ok := ParseSessionConnectionAge(positive, closing, start)
	if !ok || age.DrainAt.Sub(start) != time.Second || age.CloseAt.Sub(start) != 2*time.Second {
		t.Fatal("relative monotonic deadlines changed")
	}
}
func TestSessionConnectionAgeConservesOldHealthyWindow(t *testing.T) {
	start := time.Now()
	age, ok := ParseSessionConnectionAge([]string{strconv.FormatInt(int64(54*time.Second), 10)}, []string{strconv.FormatInt(int64(time.Minute), 10)}, start)
	if !ok {
		t.Fatal("fixture hint invalid")
	}
	request, interval := 15*time.Second, 5*time.Second
	if age.Due(start.Add(29*time.Second-time.Nanosecond), request, interval) || !age.Due(start.Add(29*time.Second), request, interval) {
		t.Fatal("probe did not cover the request and two cycles")
	}
	for _, tc := range []struct{ elapsed, want time.Duration }{{0, 15 * time.Second}, {50 * time.Second, 2 * time.Second}, {54 * time.Second, 15 * time.Second}, {time.Minute, 15 * time.Second}} {
		if got := age.ProbeBudget(start.Add(tc.elapsed), request); got != tc.want {
			t.Fatalf("at %v budget=%v want=%v", tc.elapsed, got, tc.want)
		}
	}
	short, ok := ParseSessionConnectionAge([]string{strconv.FormatInt(int64(2*time.Second), 10)}, []string{strconv.FormatInt(int64(3*time.Second), 10)}, start)
	if !ok {
		t.Fatal("short fixture hint invalid")
	}
	if short.Due(start.Add(time.Second-time.Nanosecond), request, interval) || !short.Due(start.Add(time.Second), request, interval) {
		t.Fatal("short age used more than half as its lead")
	}
	if (SessionConnectionAge{}).Due(start, request, interval) || age.Due(start, 0, interval) || age.Due(start, request, 0) {
		t.Fatal("missing policy scheduled handoff")
	}
}
