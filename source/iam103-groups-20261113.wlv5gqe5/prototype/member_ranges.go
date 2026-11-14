package directory

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	ldap "github.com/go-ldap/ldap/v3"
)

type rangePolicy struct{ MaxPages, MaxMembers, MaxBytes int }

func index(s string, maxValue int) (int, error) {
	if s == "" || len(s) > 10 || (len(s) > 1 && s[0] == '0') {
		return 0, errIncomplete
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errIncomplete
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n > uint64(maxValue) {
		return 0, errIncomplete
	}
	return int(n), nil
}

type rangeAttribute struct {
	low, high int
	terminal  bool
	values    [][]byte
}

// The decoder sees already bounded LDAP entries. It is not an AD connection profile.
func memberRanges(ctx context.Context, p rangePolicy, first []*ldap.EntryAttribute, next func(context.Context, string) ([]*ldap.EntryAttribute, error)) ([]string, error) {
	if p.MaxPages < 1 || p.MaxPages > 100 || p.MaxMembers < 1 || p.MaxMembers > 100000 || p.MaxBytes < 1 || p.MaxBytes > 16<<20 {
		return nil, errConfiguration
	}
	attrs, expected, totalBytes := first, 0, 0
	var result []string
	seen := map[string]bool{}
	for page := 0; page < p.MaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		plainSeen := false
		var plain [][]byte
		var ranges []rangeAttribute
		seenNames := map[string]bool{}
		for _, a := range attrs {
			name := strings.ToLower(a.Name)
			if name != "member" && !strings.HasPrefix(name, "member;") {
				continue
			}
			if seenNames[name] {
				return nil, errIncomplete
			}
			seenNames[name] = true
			if name == "member" {
				plainSeen, plain = true, a.ByteValues
				continue
			}
			if !strings.HasPrefix(name, "member;range=") {
				return nil, errIncomplete
			}
			parts := strings.Split(strings.TrimPrefix(name, "member;range="), "-")
			if len(parts) != 2 {
				return nil, errIncomplete
			}
			lo, err := index(parts[0], p.MaxMembers)
			if err != nil {
				return nil, err
			}
			r := rangeAttribute{low: lo, terminal: parts[1] == "*", values: a.ByteValues}
			if !r.terminal {
				r.high, err = index(parts[1], p.MaxMembers-1)
				if err != nil || r.high < lo {
					return nil, errIncomplete
				}
			}
			ranges = append(ranges, r)
		}
		var values [][]byte
		terminal := false
		if len(ranges) == 0 {
			if !plainSeen || expected != 0 {
				return nil, errIncomplete
			}
			values, terminal = plain, true
		} else {
			if plainSeen && len(plain) != 0 {
				return nil, errIncomplete
			}
			// AD may echo the requested wildcard attribute empty alongside the actual numbered range.
			if len(ranges) == 2 {
				filtered := make([]rangeAttribute, 0, 1)
				placeholders := 0
				for _, r := range ranges {
					if r.terminal && r.low == expected && len(r.values) == 0 {
						placeholders++
						continue
					}
					filtered = append(filtered, r)
				}
				if placeholders != 1 {
					return nil, errIncomplete
				}
				ranges = filtered
			}
			if len(ranges) != 1 {
				return nil, errIncomplete
			}
			r := ranges[0]
			if r.low != expected || (!r.terminal && len(r.values) != r.high-r.low+1) || (len(r.values) == 0 && (!r.terminal || expected != 0)) {
				return nil, errIncomplete
			}
			values, terminal = r.values, r.terminal
		}
		if len(values) > p.MaxMembers-len(result) {
			return nil, errIncomplete
		}
		for _, raw := range values {
			if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) || totalBytes+len(raw) > p.MaxBytes {
				return nil, errIncomplete
			}
			v := string(raw)
			if _, err := ldap.ParseDN(v); err != nil || seen[v] {
				return nil, errIncomplete
			}
			totalBytes += len(raw)
			seen[v] = true
			result = append(result, v)
		}
		if terminal {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if result == nil {
				result = []string{}
			}
			return result, nil
		}
		expected = len(result)
		if next == nil || expected >= p.MaxMembers || page+1 >= p.MaxPages {
			return nil, errIncomplete
		}
		var err error
		attrs, err = next(ctx, "member;range="+strconv.Itoa(expected)+"-*")
		if err != nil {
			return nil, err
		}
	}
	return nil, errIncomplete
}
