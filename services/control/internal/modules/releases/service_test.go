package releases

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"strings"
	"testing"
)

func historyFixture() view.Snapshot {
	s := view.Snapshot{ScopeID: strings.Repeat("a", 32), HeadSHA256: strings.Repeat("b", 64), Sequence: 10, FirstSequence: 5}
	for n := 5; n <= 10; n++ {
		s.Events = append(s.Events, view.Event{Sequence: n})
	}
	return s
}
func TestReleaseHistoryCursorBindsHeadAndBoundedSuffix(t *testing.T) {
	s := historyFixture()
	p, e := history(s, "", 2)
	if e != nil || len(p.Items) != 2 || p.Items[0].Sequence != 10 || p.NextCursor == nil {
		t.Fatal(p, e)
	}
	next, e := history(s, *p.NextCursor, 2)
	if e != nil || next.Items[0].Sequence != 8 {
		t.Fatal(next, e)
	}
	s.HeadSHA256 = strings.Repeat("c", 64)
	if _, e = history(s, *p.NextCursor, 2); e == nil {
		t.Fatal("old head reused")
	}
	s = historyFixture()
	for _, c := range []cursor{{s.ScopeID, s.HeadSHA256, 5}, {s.ScopeID, s.HeadSHA256, 11}, {strings.Repeat("d", 32), s.HeadSHA256, 8}} {
		raw, _ := json.Marshal(c)
		if _, e = history(s, base64.RawURLEncoding.EncodeToString(raw), 2); e == nil {
			t.Fatal("foreign or invalid cursor")
		}
	}
}
func TestReleaseViewAuthorityAndSameUIDFailClosed(t *testing.T) {
	s := &Service{}
	if _, e := s.Current(context.Background(), identity.Auth{}, strings.Repeat("x", 36)); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal(e)
	}
	c := Config{Directory: t.TempDir(), ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: os.Geteuid(), PageSize: 2, OperationSeconds: 1}
	if _, e := readSnapshot(c); e == nil {
		t.Fatal("same UID runtime admitted")
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.PageSize = 0 }, func(c *Config) { c.PageSize = 101 }, func(c *Config) { c.PublisherUID = -1 }, func(c *Config) { c.Directory = "relative" }, func(c *Config) { c.OperationSeconds = 31 }} {
		v := c
		mutate(&v)
		if v.Validate() == nil {
			t.Fatal("invalid policy")
		}
	}
}
