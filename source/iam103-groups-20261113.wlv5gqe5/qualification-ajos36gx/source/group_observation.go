package directory

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	ldap "github.com/go-ldap/ldap/v3"
)

var groupAttributes = []string{"entryUUID", "entryCSN", "objectClass", "member"}

type observation struct {
	c       *client
	x       *connection
	p       graphPolicy
	queries int
}

func inBase(dn, base string) bool {
	d, err := ldap.ParseDN(dn)
	b, be := ldap.ParseDN(base)
	return err == nil && be == nil && b.AncestorOfFold(d)
}

func parseObject(e *ldap.Entry, cfg config, p graphPolicy) (object, error) {
	id, err := stable(e)
	if err != nil || e.DN == "" || len(e.DN) > 4096 || !utf8.ValidString(e.DN) {
		return object{}, errIncomplete
	}
	revision, err := one(e, "entryCSN")
	if err != nil || len(revision) > 256 || strings.IndexFunc(revision, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return object{}, errIncomplete
	}
	var classes []string
	var members []string
	seenClass, seenMembers := false, false
	for _, attr := range e.Attributes {
		switch strings.ToLower(attr.Name) {
		case "objectclass":
			if seenClass || len(attr.ByteValues) == 0 || len(attr.ByteValues) > 32 {
				return object{}, errIncomplete
			}
			seenClass = true
			for _, raw := range attr.ByteValues {
				if len(raw) == 0 || len(raw) > 128 || !utf8.Valid(raw) {
					return object{}, errIncomplete
				}
				classes = append(classes, strings.ToLower(string(raw)))
			}
		case "member":
			if seenMembers || len(attr.ByteValues) > p.MaxEdges {
				return object{}, errIncomplete
			}
			seenMembers = true
			for _, raw := range attr.ByteValues {
				if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
					return object{}, errIncomplete
				}
				if _, err := ldap.ParseDN(string(raw)); err != nil {
					return object{}, errIncomplete
				}
				members = append(members, string(raw))
			}
		default:
			if strings.HasPrefix(strings.ToLower(attr.Name), "member;") || strings.HasPrefix(strings.ToLower(attr.Name), "objectclass;") {
				return object{}, errIncomplete
			}
		}
	}
	user, group := slices.Contains(classes, "inetorgperson"), slices.Contains(classes, "groupofnames")
	if user == group || slices.Contains(classes, "alias") || slices.Contains(classes, "referral") {
		return object{}, errIncomplete
	}
	kind := "user"
	if group {
		kind = "group"
		if !inBase(e.DN, cfg.GroupBase) || len(members) == 0 {
			return object{}, errIncomplete
		}
	} else if !inBase(e.DN, cfg.UserBase) || seenMembers {
		return object{}, errIncomplete
	}
	slices.Sort(members)
	return object{DN: e.DN, ID: id, Revision: revision, Kind: kind, Members: members}, nil
}

func sameObject(a, b object) bool {
	return a.ID == b.ID && a.DN == b.DN && a.Revision == b.Revision && a.Kind == b.Kind && slices.Equal(a.Members, b.Members)
}

func (o *observation) search(ctx context.Context, q *ldap.SearchRequest) (*ldap.SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.queries++
	if o.queries > o.p.MaxSearches {
		return nil, errIncomplete
	}
	r, err := o.x.sdk.Search(q)
	if err != nil || r == nil || len(r.Referrals) != 0 {
		return nil, operationError(ctx, errIncomplete)
	}
	return r, nil
}

func (o *observation) read(ctx context.Context, dn string) (object, error) {
	if len(dn) > 4096 || (!inBase(dn, o.c.cfg.UserBase) && !inBase(dn, o.c.cfg.GroupBase)) {
		return object{}, errIncomplete
	}
	q := ldap.NewSearchRequest(dn, ldap.ScopeBaseObject, ldap.NeverDerefAliases, 1, 0, false, "(objectClass=*)", groupAttributes, nil)
	q.EnforceSizeLimit = true
	r, err := o.search(ctx, q)
	if err != nil {
		return object{}, err
	}
	if len(r.Entries) != 1 || len(r.Controls) != 0 {
		return object{}, errIncomplete
	}
	want, _ := ldap.ParseDN(dn)
	got, pe := ldap.ParseDN(r.Entries[0].DN)
	if pe != nil || !want.EqualFold(got) {
		return object{}, errIncomplete
	}
	return parseObject(r.Entries[0], o.c.cfg, o.p)
}

func (o *observation) scan(ctx context.Context) (map[string]object, error) {
	all := map[string]object{}
	var cookie []byte
	seen := map[string]bool{}
	for page := 0; page < o.c.cfg.MaxPages; page++ {
		control := ldap.NewControlPaging(uint32(o.c.cfg.PageSize))
		control.SetCookie(cookie)
		q := ldap.NewSearchRequest(o.c.cfg.GroupBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, o.c.cfg.MaxEntries, 0, false, "(objectClass=groupOfNames)", groupAttributes, []ldap.Control{control})
		q.EnforceSizeLimit = true
		r, err := o.search(ctx, q)
		if err != nil {
			return nil, err
		}
		if len(r.Entries) > o.c.cfg.PageSize || len(r.Controls) != 1 {
			return nil, errIncomplete
		}
		next, ok := r.Controls[0].(*ldap.ControlPaging)
		if !ok || len(next.Cookie) > o.c.cfg.CookieBytes {
			return nil, errIncomplete
		}
		for _, raw := range r.Entries {
			n, err := parseObject(raw, o.c.cfg, o.p)
			if err != nil || n.Kind != "group" || all[n.ID].ID != "" || len(all) >= o.c.cfg.MaxEntries || len(all) >= o.p.MaxObjects {
				return nil, errIncomplete
			}
			all[n.ID] = n
		}
		if len(next.Cookie) == 0 {
			return all, nil
		}
		if len(r.Entries) == 0 || seen[string(next.Cookie)] {
			return nil, errIncomplete
		}
		seen[string(next.Cookie)] = true
		cookie = slices.Clone(next.Cookie)
	}
	return nil, errIncomplete
}

// phase is external-experiment instrumentation, never a serving API.
func (c *client) observeGroups(ctx context.Context, p graphPolicy, phase func(string)) (*groupGraph, error) {
	if !p.valid() {
		return nil, errConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	started := time.Now()
	x, err := c.searchConnection(ctx)
	if err != nil {
		return nil, err
	}
	defer x.close()
	o := observation{c: c, x: x, p: p}
	groups, err := o.scan(ctx)
	if err != nil {
		return nil, err
	}
	if phase != nil {
		phase("collected")
	}
	nodes := map[string]object{}
	for id, n := range groups {
		nodes[id] = n
	}
	direct := map[string][]string{}
	byDN := map[string]object{}
	edges := 0
	for id, g := range groups {
		for _, dn := range g.Members {
			edges++
			if edges > p.MaxEdges {
				return nil, errIncomplete
			}
			n, ok := byDN[dn]
			if !ok {
				n, err = o.read(ctx, dn)
				if err != nil {
					return nil, err
				}
				byDN[dn] = n
			}
			if n.Kind == "group" && !sameObject(groups[n.ID], n) {
				return nil, errIncomplete
			}
			if prior, ok := nodes[n.ID]; ok && !sameObject(prior, n) {
				return nil, errIncomplete
			}
			if _, ok := nodes[n.ID]; !ok && len(nodes) >= p.MaxObjects {
				return nil, errIncomplete
			}
			nodes[n.ID] = n
			direct[id] = append(direct[id], n.ID)
		}
	}
	effective, err := closure(ctx, p, nodes, direct)
	if err != nil {
		return nil, err
	}
	if phase != nil {
		phase("resolved")
	}
	for _, n := range byDN {
		current, err := o.read(ctx, n.DN)
		if err != nil {
			return nil, err
		}
		if !sameObject(n, current) {
			return nil, errIncomplete
		}
	}
	if phase != nil {
		phase("rechecked")
	}
	current, err := o.scan(ctx)
	if err != nil {
		return nil, err
	}
	if len(groups) != len(current) {
		return nil, errIncomplete
	}
	for id, g := range groups {
		if !sameObject(g, current[id]) {
			return nil, errIncomplete
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &groupGraph{Objects: nodes, Direct: direct, Effective: effective, ObservedFrom: started, Until: time.Now()}, nil
}
