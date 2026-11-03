package http

import (
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"strings"
)

func datasetsHumanOwns(path string) bool {
	var p []string
	if strings.HasPrefix(path, "/api/v1/admin/datasets") {
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/admin/"), "/")
		if p[0] != "datasets" {
			return false
		}
		p = p[1:]
	} else if strings.HasPrefix(path, "/api/v1/workspaces/") {
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		if len(p) < 2 || !wire.ValidID(p[0]) || p[1] != "datasets" {
			return false
		}
		p = p[2:]
	} else {
		return false
	}
	if len(p) == 0 {
		return true
	}
	if len(p) == 1 && p[0] == "configuration" {
		return true
	}
	if !wire.ValidID(p[0]) {
		return false
	}
	p = p[1:]
	if len(p) == 0 {
		return true
	}
	if len(p) == 1 {
		return p[0] == "drafts" || p[0] == "versions" || p[0] == "retire"
	}
	if !wire.ValidID(p[1]) {
		return false
	}
	switch p[0] {
	case "jobs":
		return len(p) == 2 || len(p) == 3 && (p[2] == "cancel" || p[2] == "retry")
	case "drafts":
		return len(p) == 2 || len(p) == 3 && (p[2] == "source" || p[2] == "cancel" || p[2] == "rows" || p[2] == "publications") || len(p) == 4 && p[2] == "rows" && wire.ValidID(p[3])
	case "versions":
		return len(p) == 2 || len(p) == 3 && (p[2] == "drafts" || p[2] == "retire") || len(p) == 4 && p[2] == "files" && (p[3] == "samples" || p[3] == "train" || p[3] == "validation" || p[3] == "assignments" || p[3] == "duplicates" || p[3] == "manifest")
	}
	return false
}
