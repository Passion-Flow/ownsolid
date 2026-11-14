package directory

import (
	"context"
	"slices"
	"time"
)

type graphPolicy struct {
	MaxObjects, MaxEdges, MaxClosure, MaxDepth, MaxSearches int
}

func (p graphPolicy) valid() bool {
	return p.MaxObjects > 0 && p.MaxObjects <= 10000 && p.MaxEdges > 0 && p.MaxEdges <= 100000 &&
		p.MaxClosure > 0 && p.MaxClosure <= 100000 && p.MaxDepth > 0 && p.MaxDepth <= 32 &&
		p.MaxSearches > 0 && p.MaxSearches <= 50000
}

type object struct {
	DN, ID, Revision, Kind string
	Members                []string
}

type groupGraph struct {
	Objects             map[string]object
	Direct, Effective   map[string][]string
	ObservedFrom, Until time.Time
}

// Closure is derived from stable identifiers only. It grants no local role.
func closure(ctx context.Context, p graphPolicy, nodes map[string]object, direct map[string][]string) (map[string][]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.valid() || len(nodes) > p.MaxObjects {
		return nil, errIncomplete
	}
	edges := 0
	for id, n := range nodes {
		if id == "" || id != n.ID || (n.Kind != "group" && n.Kind != "user") || (n.Kind == "user" && len(direct[id]) != 0) {
			return nil, errIncomplete
		}
	}
	for id, members := range direct {
		if nodes[id].Kind != "group" {
			return nil, errIncomplete
		}
		seen := map[string]bool{}
		for _, member := range members {
			edges++
			if seen[member] || nodes[member].ID == "" || edges > p.MaxEdges {
				return nil, errIncomplete
			}
			seen[member] = true
		}
	}
	state, heights := map[string]int{}, map[string]int{}
	result := map[string][]string{}
	derived := 0
	var visit func(string, int) ([]string, int, error)
	visit = func(id string, depth int) ([]string, int, error) {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if depth > p.MaxDepth || state[id] == 1 {
			return nil, 0, errIncomplete
		}
		if state[id] == 2 {
			if depth+heights[id]-1 > p.MaxDepth {
				return nil, 0, errIncomplete
			}
			return result[id], heights[id], nil
		}
		state[id] = 1
		users := map[string]bool{}
		height := 1
		for _, member := range direct[id] {
			if nodes[member].Kind == "user" {
				if !users[member] && derived+len(users)+1 > p.MaxClosure {
					return nil, 0, errIncomplete
				}
				users[member] = true
				continue
			}
			children, childHeight, err := visit(member, depth+1)
			if err != nil {
				return nil, 0, err
			}
			height = max(height, childHeight+1)
			for _, user := range children {
				if !users[user] && derived+len(users)+1 > p.MaxClosure {
					return nil, 0, errIncomplete
				}
				users[user] = true
			}
		}
		if derived+len(users) > p.MaxClosure {
			return nil, 0, errIncomplete
		}
		derived += len(users)
		members := make([]string, 0, len(users))
		for user := range users {
			members = append(members, user)
		}
		slices.Sort(members)
		result[id], heights[id], state[id] = members, height, 2
		return members, height, nil
	}
	ids := make([]string, 0, len(direct))
	for id, n := range nodes {
		if n.Kind == "group" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		if _, _, err := visit(id, 1); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
