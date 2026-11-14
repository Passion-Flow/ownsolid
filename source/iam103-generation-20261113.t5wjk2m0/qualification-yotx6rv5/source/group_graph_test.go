package directory

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	ldap "github.com/go-ldap/ldap/v3"
)

func graphLimits() graphPolicy {
	return graphPolicy{MaxObjects: 40, MaxEdges: 100, MaxClosure: 100, MaxDepth: 8, MaxSearches: 200}
}
func smallGraph() (map[string]object, map[string][]string) {
	return map[string]object{"a": {ID: "a", Kind: "group"}, "b": {ID: "b", Kind: "group"}, "u": {ID: "u", Kind: "user"}, "v": {ID: "v", Kind: "user"}}, map[string][]string{"a": {"b", "u"}, "b": {"u", "v"}}
}
func TestStableGraphClosure(t *testing.T) {
	nodes, edges := smallGraph()
	got, err := closure(context.Background(), graphLimits(), nodes, edges)
	if err != nil || !slices.Equal(got["a"], []string{"u", "v"}) || !slices.Equal(got["b"], []string{"u", "v"}) {
		t.Fatal("bounded stable closure mismatch", err)
	}
	empty, err := closure(context.Background(), graphLimits(), map[string]object{}, map[string][]string{})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("complete empty graph lost", err)
	}
}
func TestGraphRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*graphPolicy, map[string]object, map[string][]string)
	}{
		{"cycle", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["b"] = []string{"a"} }},
		{"self_cycle", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["a"] = []string{"a"} }},
		{"unknown_reference", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["a"] = []string{"missing"} }},
		{"duplicate_edge", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["a"] = []string{"u", "u"} }},
		{"user_edges", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["u"] = []string{"v"} }},
		{"foreign_parent", func(_ *graphPolicy, _ map[string]object, e map[string][]string) { e["missing"] = []string{"u"} }},
		{"object_limit", func(p *graphPolicy, _ map[string]object, _ map[string][]string) { p.MaxObjects = 3 }},
		{"edge_limit", func(p *graphPolicy, _ map[string]object, _ map[string][]string) { p.MaxEdges = 3 }},
		{"closure_limit", func(p *graphPolicy, _ map[string]object, _ map[string][]string) { p.MaxClosure = 3 }},
		{"depth", func(p *graphPolicy, _ map[string]object, _ map[string][]string) { p.MaxDepth = 1 }},
		{"memoized_height", func(p *graphPolicy, n map[string]object, e map[string][]string) {
			p.MaxDepth = 2
			n["z"] = object{ID: "z", Kind: "group"}
			e["z"] = []string{"a"}
		}},
		{"invalid_kind", func(_ *graphPolicy, n map[string]object, _ map[string][]string) {
			n["u"] = object{ID: "u", Kind: "alias"}
		}},
		{"mismatched_id", func(_ *graphPolicy, n map[string]object, _ map[string][]string) {
			n["u"] = object{ID: "v", Kind: "user"}
		}},
		{"invalid_policy", func(p *graphPolicy, _ map[string]object, _ map[string][]string) { p.MaxDepth = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := graphLimits()
			n, e := smallGraph()
			tc.edit(&p, n, e)
			got, err := closure(context.Background(), p, n, e)
			if !errors.Is(err, errIncomplete) || got != nil {
				t.Fatal("partial graph escaped refusal", err)
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		n, e := smallGraph()
		got, err := closure(ctx, graphLimits(), n, e)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal("canceled graph escaped", err)
		}
	})
}
func TestConcurrentStableGraphs(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := smallGraph()
			got, err := closure(context.Background(), graphLimits(), n, e)
			if err != nil || len(got["a"]) != 2 {
				t.Error("concurrent graph failed", err)
			}
		}()
	}
	wg.Wait()
}

func att(name string, values ...string) *ldap.EntryAttribute {
	a := &ldap.EntryAttribute{Name: name, Values: values}
	for _, v := range values {
		a.ByteValues = append(a.ByteValues, []byte(v))
	}
	return a
}

var dn0 = "uid=zero,ou=people,dc=fixture,dc=test"
var dn1 = "uid=one,ou=people,dc=fixture,dc=test"
var dn2 = "uid=two,ou=people,dc=fixture,dc=test"

func rangeLimits() rangePolicy { return rangePolicy{MaxPages: 5, MaxMembers: 10, MaxBytes: 4096} }
func TestMemberRangeContinuation(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) {
		got, err := memberRanges(context.Background(), rangeLimits(), []*ldap.EntryAttribute{att("member", dn0, dn1)}, nil)
		if err != nil || !slices.Equal(got, []string{dn0, dn1}) {
			t.Fatal(err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		got, err := memberRanges(context.Background(), rangeLimits(), []*ldap.EntryAttribute{att("member")}, nil)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatal(err)
		}
	})
	t.Run("numbered_and_empty_requested_attribute", func(t *testing.T) {
		calls := 0
		got, err := memberRanges(context.Background(), rangeLimits(), []*ldap.EntryAttribute{att("member"), att("MEMBER;range=0-0", dn0)}, func(ctx context.Context, request string) ([]*ldap.EntryAttribute, error) {
			calls++
			if request == "member;range=1-*" {
				return []*ldap.EntryAttribute{att(request), att("member;range=1-1", dn1)}, nil
			}
			if request == "member;range=2-*" {
				return []*ldap.EntryAttribute{att(request, dn2)}, nil
			}
			t.Fatal("wrong continuation", request)
			return nil, errIncomplete
		})
		if err != nil || calls != 2 || !slices.Equal(got, []string{dn0, dn1, dn2}) {
			t.Fatal("range sequence failed", err)
		}
	})
}
func TestMemberRangeRefusals(t *testing.T) {
	cases := []struct {
		name       string
		attrs      []*ldap.EntryAttribute
		next       []*ldap.EntryAttribute
		policy     func(*rangePolicy)
		fetchError bool
	}{
		{name: "missing_attribute"},
		{name: "duplicate_plain", attrs: []*ldap.EntryAttribute{att("member", dn0), att("MEMBER", dn1)}},
		{name: "ordinary_and_range", attrs: []*ldap.EntryAttribute{att("member", dn0), att("member;range=0-*", dn1)}},
		{name: "gap", attrs: []*ldap.EntryAttribute{att("member;range=1-*", dn0)}},
		{name: "reverse", attrs: []*ldap.EntryAttribute{att("member;range=2-1", dn0)}},
		{name: "bad_count", attrs: []*ldap.EntryAttribute{att("member;range=0-1", dn0)}},
		{name: "plus", attrs: []*ldap.EntryAttribute{att("member;range=+0-*", dn0)}},
		{name: "leading_zero", attrs: []*ldap.EntryAttribute{att("member;range=00-*", dn0)}},
		{name: "overflow", attrs: []*ldap.EntryAttribute{att("member;range=4294967296-*", dn0)}},
		{name: "unsupported_option", attrs: []*ldap.EntryAttribute{att("member;binary", dn0)}},
		{name: "extra_options", attrs: []*ldap.EntryAttribute{att("member;range=0-*;binary", dn0)}},
		{name: "two_ranges", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0), att("member;range=1-*", dn1)}},
		{name: "duplicate_range", attrs: []*ldap.EntryAttribute{att("member;range=0-*", dn0), att("MEMBER;range=0-*", dn1)}},
		{name: "duplicate_member", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}, next: []*ldap.EntryAttribute{att("member;range=1-*", dn0)}},
		{name: "overlap", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}, next: []*ldap.EntryAttribute{att("member;range=0-*", dn1)}},
		{name: "missing_continuation", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}},
		{name: "empty_continuation", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}, next: []*ldap.EntryAttribute{att("member;range=1-*")}},
		{name: "fetch_failure", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}, fetchError: true},
		{name: "invalid_dn", attrs: []*ldap.EntryAttribute{att("member", "not a DN")}},
		{name: "invalid_utf8", attrs: []*ldap.EntryAttribute{att("member", string([]byte{0xff}))}},
		{name: "member_budget", attrs: []*ldap.EntryAttribute{att("member", dn0, dn1)}, policy: func(p *rangePolicy) { p.MaxMembers = 1 }},
		{name: "byte_budget", attrs: []*ldap.EntryAttribute{att("member", dn0, dn1)}, policy: func(p *rangePolicy) { p.MaxBytes = len(dn0) }},
		{name: "page_budget", attrs: []*ldap.EntryAttribute{att("member;range=0-0", dn0)}, next: []*ldap.EntryAttribute{att("member;range=1-*", dn1)}, policy: func(p *rangePolicy) { p.MaxPages = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := rangeLimits()
			if tc.policy != nil {
				tc.policy(&p)
			}
			got, err := memberRanges(context.Background(), p, tc.attrs, func(context.Context, string) ([]*ldap.EntryAttribute, error) {
				if tc.fetchError {
					return nil, errUnavailable
				}
				return tc.next, nil
			})
			if err == nil || got != nil {
				t.Fatal("partial range escaped", err)
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := memberRanges(ctx, rangeLimits(), []*ldap.EntryAttribute{att("member", dn0)}, nil)
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal(err)
		}
	})
}
func TestGroupObjectProfileRefusals(t *testing.T) {
	cfg := config{UserBase: "ou=people,dc=fixture,dc=test", GroupBase: "ou=groups,dc=fixture,dc=test"}
	makeEntry := func() *ldap.Entry {
		return &ldap.Entry{DN: "cn=g," + cfg.GroupBase, Attributes: []*ldap.EntryAttribute{att("entryUUID", "e1234567-1234-1234-1234-123456789abc"), att("entryCSN", "20261113000000.000001Z#000000#000#000000"), att("objectClass", "top", "groupOfNames"), att("member", dn0)}}
	}
	if _, err := parseObject(makeEntry(), cfg, graphLimits()); err != nil {
		t.Fatal("positive group refused", err)
	}
	cases := []struct {
		name string
		edit func(*ldap.Entry)
	}{
		{"out_of_base", func(e *ldap.Entry) { e.DN = "cn=g,ou=elsewhere,dc=fixture,dc=test" }},
		{"ambiguous_class", func(e *ldap.Entry) { e.Attributes[2] = att("objectClass", "groupOfNames", "inetOrgPerson") }},
		{"alias", func(e *ldap.Entry) { e.Attributes[2] = att("objectClass", "alias") }},
		{"alias_combined_with_group", func(e *ldap.Entry) { e.Attributes[2] = att("objectClass", "alias", "groupOfNames") }},
		{"missing_members", func(e *ldap.Entry) { e.Attributes = e.Attributes[:3] }},
		{"unreadable_revision", func(e *ldap.Entry) { e.Attributes[1] = att("entryCSN") }},
		{"ambiguous_revision", func(e *ldap.Entry) { e.Attributes = append(e.Attributes, att("entryCSN", "other")) }},
		{"ranged_member_in_ldap_profile", func(e *ldap.Entry) { e.Attributes[3] = att("member;range=0-*", dn0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := makeEntry()
			tc.edit(e)
			got, err := parseObject(e, cfg, graphLimits())
			if err == nil || got.ID != "" {
				t.Fatal("invalid object accepted", err)
			}
		})
	}
}
