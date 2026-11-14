package directory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	ldap "github.com/go-ldap/ldap/v3"
)

type groupFixture struct {
	c                         *client
	admin                     *ldap.Conn
	child, parent, bob, alice string
	owned                     []string
}

func newGroupFixture(t *testing.T) *groupFixture {
	t.Helper()
	c, f := live(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	g := &groupFixture{c: c, admin: fixtureWriter(t, c, ctx, f.AdminDN, f.AdminPasswordFile), child: "cn=own-child," + c.cfg.GroupBase, parent: "cn=own-parent," + c.cfg.GroupBase, bob: "uid=own-bob," + c.cfg.UserBase, alice: "uid=alice," + c.cfg.UserBase}
	t.Cleanup(func() {
		for i := len(g.owned) - 1; i >= 0; i-- {
			err := g.admin.Del(ldap.NewDelRequest(g.owned[i], nil))
			if err != nil && !ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
				t.Error("owned fixture cleanup failed")
			}
		}
	})
	q := ldap.NewAddRequest("ou=own-other,"+c.cfg.UserBase, nil)
	q.Attribute("objectClass", []string{"organizationalUnit"})
	q.Attribute("ou", []string{"own-other"})
	if err := g.admin.Add(q); err != nil {
		t.Fatal("fixture OU add failed")
	}
	g.owned = append(g.owned, "ou=own-other,"+c.cfg.UserBase)
	g.addUser(t, g.bob)
	g.addGroup(t, g.child, g.alice, g.bob)
	g.addGroup(t, g.parent, g.child, g.alice)
	return g
}

func (g *groupFixture) addUser(t *testing.T, dn string) {
	t.Helper()
	q := ldap.NewAddRequest(dn, nil)
	q.Attribute("objectClass", []string{"inetOrgPerson"})
	q.Attribute("cn", []string{"Own group fixture"})
	q.Attribute("sn", []string{"Fixture"})
	q.Attribute("uid", []string{strings.Split(strings.Split(dn, ",")[0], "=")[1]})
	if err := g.admin.Add(q); err != nil {
		t.Fatal("fixture user add failed")
	}
	g.owned = append(g.owned, dn)
}
func (g *groupFixture) addGroup(t *testing.T, dn string, members ...string) {
	t.Helper()
	q := ldap.NewAddRequest(dn, nil)
	q.Attribute("objectClass", []string{"groupOfNames"})
	q.Attribute("cn", []string{strings.Split(strings.Split(dn, ",")[0], "=")[1]})
	q.Attribute("member", members)
	if err := g.admin.Add(q); err != nil {
		t.Fatal("fixture group add failed")
	}
	g.owned = append(g.owned, dn)
}
func (g *groupFixture) replace(t *testing.T, dn string, members ...string) {
	t.Helper()
	q := ldap.NewModifyRequest(dn, nil)
	q.Replace("member", members)
	if err := g.admin.Modify(q); err != nil {
		t.Fatal("fixture members replace failed")
	}
}
func (g *groupFixture) del(t *testing.T, dn string) {
	t.Helper()
	if err := g.admin.Del(ldap.NewDelRequest(dn, nil)); err != nil {
		t.Fatal("fixture delete failed")
	}
}
func findObject(t *testing.T, g *groupGraph, dn string) object {
	t.Helper()
	for _, n := range g.Objects {
		if n.DN == dn {
			return n
		}
	}
	t.Fatal("expected object absent")
	return object{}
}
func mustObserve(t *testing.T, c *client) *groupGraph {
	t.Helper()
	g, err := c.observeGroups(context.Background(), graphLimits(), nil)
	if err != nil || g == nil {
		t.Fatal("complete observation failed", err)
	}
	if g.ObservedFrom.After(g.Until) || time.Since(g.ObservedFrom) > c.cfg.Timeout {
		t.Fatal("observation interval invalid")
	}
	return g
}
func TestLiveNestedGroupStableIdentity(t *testing.T) {
	f := newGroupFixture(t)
	before := mustObserve(t, f.c)
	child, parent, bob, alice := findObject(t, before, f.child), findObject(t, before, f.parent), findObject(t, before, f.bob), findObject(t, before, f.alice)
	want := []string{bob.ID, alice.ID}
	slices.Sort(want)
	if !slices.Equal(before.Effective[child.ID], want) || !slices.Equal(before.Effective[parent.ID], want) || len(before.Direct[parent.ID]) != 2 {
		t.Fatal("nested stable membership mismatch")
	}
	newDN := "cn=own-renamed," + f.c.cfg.GroupBase
	if err := f.admin.ModifyDN(ldap.NewModifyDNRequest(f.child, "cn=own-renamed", true, "")); err != nil {
		t.Fatal("fixture group rename failed")
	}
	f.owned = append(f.owned, newDN)
	if got, err := f.c.observeGroups(context.Background(), graphLimits(), nil); err == nil || got != nil {
		t.Fatal("dangling old DN accepted", err)
	}
	f.replace(t, f.parent, newDN, f.alice)
	after := mustObserve(t, f.c)
	if findObject(t, after, newDN).ID != child.ID || !slices.Equal(after.Effective[parent.ID], want) {
		t.Fatal("rename changed stable group identity")
	}
	f.del(t, f.bob)
	f.addUser(t, f.bob)
	recreated := mustObserve(t, f.c)
	newBob := findObject(t, recreated, f.bob)
	if newBob.ID == bob.ID || slices.Contains(recreated.Effective[parent.ID], bob.ID) || !slices.Contains(recreated.Effective[parent.ID], newBob.ID) {
		t.Fatal("recreated member inherited old identity")
	}
}
func TestLiveNestedGroupFailureRetry(t *testing.T) {
	cases := []struct {
		name   string
		member func(*groupFixture) string
	}{
		{"cycle", func(f *groupFixture) string { return f.parent }},
		{"unresolved", func(f *groupFixture) string { return "uid=missing," + f.c.cfg.UserBase }},
		{"outside_approved_bases", func(f *groupFixture) string { return f.c.cfg.SearchDN }},
		{"unsupported_class", func(f *groupFixture) string { return "ou=own-other," + f.c.cfg.UserBase }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGroupFixture(t)
			mustObserve(t, f.c)
			f.replace(t, f.child, tc.member(f))
			got, err := f.c.observeGroups(context.Background(), graphLimits(), nil)
			if err == nil || got != nil {
				t.Fatal("partial graph escaped", err)
			}
			f.replace(t, f.child, f.alice, f.bob)
			mustObserve(t, f.c)
		})
	}
}
func TestLiveGroupObservedChurn(t *testing.T) {
	cases := []struct {
		name, phase string
		mutate      func(*testing.T, *groupFixture)
	}{
		{"changed_group", "collected", func(t *testing.T, f *groupFixture) { f.replace(t, f.child, f.alice) }},
		{"recreated_group", "collected", func(t *testing.T, f *groupFixture) { f.del(t, f.child); f.addGroup(t, f.child, f.alice, f.bob) }},
		{"recreated_user", "resolved", func(t *testing.T, f *groupFixture) { f.del(t, f.bob); f.addUser(t, f.bob) }},
		{"added_group", "rechecked", func(t *testing.T, f *groupFixture) { f.addGroup(t, "cn=own-extra,"+f.c.cfg.GroupBase, f.alice) }},
		{"removed_group", "rechecked", func(t *testing.T, f *groupFixture) { f.del(t, f.child) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGroupFixture(t)
			called := false
			got, err := f.c.observeGroups(context.Background(), graphLimits(), func(phase string) {
				if phase == tc.phase {
					called = true
					tc.mutate(t, f)
				}
			})
			if !called || err == nil || got != nil {
				t.Fatal("observed churn exposed candidate", err)
			}
			if tc.name == "removed_group" {
				f.replace(t, f.parent, f.alice)
			}
			mustObserve(t, f.c)
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		f := newGroupFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		got, err := f.c.observeGroups(ctx, graphLimits(), func(phase string) {
			if phase == "resolved" {
				cancel()
			}
		})
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatal("canceled observation escaped", err)
		}
		mustObserve(t, f.c)
	})
}
func TestLiveGraphBudgetsAndCompleteEmpty(t *testing.T) {
	f := newGroupFixture(t)
	cases := []struct {
		name string
		edit func(*graphPolicy)
	}{
		{"searches", func(p *graphPolicy) { p.MaxSearches = 1 }},
		{"objects", func(p *graphPolicy) { p.MaxObjects = 5 }},
		{"edges", func(p *graphPolicy) { p.MaxEdges = 1 }},
		{"closure", func(p *graphPolicy) { p.MaxClosure = 1 }},
		{"depth", func(p *graphPolicy) { p.MaxDepth = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := graphLimits()
			tc.edit(&p)
			got, err := f.c.observeGroups(context.Background(), p, nil)
			if err == nil || got != nil {
				t.Fatal("budget exposed partial graph", err)
			}
			mustObserve(t, f.c)
		})
	}
	emptyDN := "ou=own-empty,dc=fixture,dc=test"
	q := ldap.NewAddRequest(emptyDN, nil)
	q.Attribute("objectClass", []string{"organizationalUnit"})
	q.Attribute("ou", []string{"own-empty"})
	if err := f.admin.Add(q); err != nil {
		t.Fatal("empty fixture base add failed")
	}
	f.owned = append(f.owned, emptyDN)
	cfg := f.c.cfg
	cfg.GroupBase = emptyDN
	c, err := newClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := mustObserve(t, c)
	if len(got.Objects) != 0 || len(got.Effective) != 0 || got.Effective == nil {
		t.Fatal("complete empty observation lost")
	}
}
