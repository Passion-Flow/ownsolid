package monitoring_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// These helpers qualify test processes only. Installed product admission must
// bind the equivalent profile to a verified installation/source identity.
func admitDailyCardinalityFixture(argv []string) error {
	if len(argv) < 2 {
		return fmt.Errorf("missing actual cardinality fixture command")
	}
	required := map[string]string{
		"disablePerDayIndex":              "false",
		"search.maxTSDBStatusSeries":      "10000",
		"search.maxStatusRequestDuration": "5s",
		"search.maxTSDBStatusTopNSeries":  "3",
		"retentionPeriod":                 "7d",
		"memory.allowedBytes":             "192MB",
	}
	seen := make(map[string]bool, len(required))
	for _, arg := range argv[1:] {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		want, bounded := required[name]
		if !bounded {
			continue
		}
		if seen[name] || arg != "-"+name+"="+want {
			return fmt.Errorf("unqualified cardinality fixture flag %s", name)
		}
		seen[name] = true
	}
	for name := range required {
		if !seen[name] {
			return fmt.Errorf("missing cardinality fixture flag %s", name)
		}
	}
	return nil
}

// Use completed UTC days in one monthly partition, even on the first two days
// of a month. An empty day in that partition distinguishes global counts too.
func cardinalityFixtureDays(now time.Time) (first, second, empty time.Time) {
	second = now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	if second.Day() == 1 {
		second = second.AddDate(0, 0, -1)
	}
	first = second.AddDate(0, 0, -1)
	empty = first.AddDate(0, 0, -1)
	if first.Day() == 1 {
		empty = second.AddDate(0, 0, 1)
	}
	return
}

func TestDailyCardinalityFixtureAdmission(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"/fixture/victoria-metrics"}} {
		if err := admitDailyCardinalityFixture(args); err == nil {
			t.Fatal("admitted a missing actual command")
		}
	}
	qualified := []string{"/fixture/victoria-metrics", "-disablePerDayIndex=false", "-search.maxTSDBStatusSeries=10000", "-search.maxStatusRequestDuration=5s", "-search.maxTSDBStatusTopNSeries=3", "-retentionPeriod=7d", "-memory.allowedBytes=192MB", "-search.disableCache", "-httpListenAddr=127.0.0.1:12345"}
	if err := admitDailyCardinalityFixture(qualified); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{"", "-disablePerDayIndex", "-disablePerDayIndex=true", "--disablePerDayIndex=false", "-disablePerDayIndex=0", "-disablePerDayIndex=False"} {
		t.Run("index/"+replacement, func(t *testing.T) {
			args := append([]string(nil), qualified...)
			args[1] = replacement
			if err := admitDailyCardinalityFixture(args); err == nil {
				t.Fatal("admitted an absent, disabled or noncanonical actual index flag")
			}
		})
	}
	for _, extra := range []string{"-disablePerDayIndex=false", "-disablePerDayIndex=true", "--disablePerDayIndex=true", "-search.maxTSDBStatusSeries=20000", "-search.maxStatusRequestDuration=1h", "-search.maxTSDBStatusTopNSeries=100", "-retentionPeriod=90d", "-memory.allowedBytes=1GB"} {
		t.Run("override/"+extra, func(t *testing.T) {
			if err := admitDailyCardinalityFixture(append(append([]string(nil), qualified...), extra)); err == nil {
				t.Fatal("admitted an ambiguous or changed actual profile")
			}
		})
	}
	for index := 1; index <= 6; index++ {
		args := append([]string(nil), qualified...)
		args = append(args[:index], args[index+1:]...)
		if err := admitDailyCardinalityFixture(args); err == nil {
			t.Fatalf("admitted missing profile input %s", qualified[index])
		}
	}
}

func TestCardinalityFixtureUTCPartitionDays(t *testing.T) {
	for _, test := range []struct{ now, first, second, empty string }{
		{"2026-11-13T00:00:00Z", "2026-11-11", "2026-11-12", "2026-11-10"},
		{"2026-11-13T23:59:59Z", "2026-11-11", "2026-11-12", "2026-11-10"},
		{"2027-01-01T00:00:00Z", "2026-12-30", "2026-12-31", "2026-12-29"},
		{"2027-01-02T00:00:00Z", "2026-12-30", "2026-12-31", "2026-12-29"},
		{"2027-01-03T00:00:00Z", "2027-01-01", "2027-01-02", "2027-01-03"},
		{"2028-03-01T00:00:00Z", "2028-02-28", "2028-02-29", "2028-02-27"},
		{"2027-03-01T00:00:00Z", "2027-02-27", "2027-02-28", "2027-02-26"},
		{"2027-01-03T00:30:00+08:00", "2026-12-30", "2026-12-31", "2026-12-29"},
	} {
		t.Run(test.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			first, second, empty := cardinalityFixtureDays(now)
			if first.Format("2006-01-02") != test.first || second.Format("2006-01-02") != test.second || empty.Format("2006-01-02") != test.empty {
				t.Fatalf("unexpected UTC days: %s, %s, %s", first, second, empty)
			}
			for _, day := range []time.Time{first, second, empty} {
				if day.Location() != time.UTC || day.Format("2006-01") != first.Format("2006-01") || day.Before(now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -6)) || day.After(now.UTC().Truncate(24*time.Hour)) {
					t.Fatalf("day outside the same UTC partition/raw horizon: %s", day)
				}
			}
		})
	}
}
