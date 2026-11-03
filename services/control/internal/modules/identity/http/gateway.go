package http

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	distributionhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution/http"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"strings"
)

// Gateway composition extends models with current admission, never later cutovers,
// training, derived-model or Registry owners. Retirement includes retained aliases.
func (h *Handler) composeGateway() {
	d := &distribution.Service{DB: h.Service.DB, Identity: h.Service, Workspaces: h.Workspaces.Service, Nodes: h.Nodes.Service, Deployments: h.Deployments.Service}
	h.Distribution = &distributionhttp.Handler{Service: d}
	h.Models.Service.Referenced = d.HasModelReferences
}
func gatewayMachineOwns(path string) bool {
	const prefix = "/gateway/v1/instances/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	p := strings.Split(strings.TrimPrefix(path, prefix), "/")
	return len(p) == 2 && wire.ValidID(p[0]) && (p[1] == "models" || p[1] == "admissions" || p[1] == "outcomes")
}
func gatewayHumanOwns(path string) bool {
	var p []string
	admin := strings.HasPrefix(path, "/api/v1/admin/distribution/workspaces/")
	if admin {
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/admin/distribution/workspaces/"), "/")
	} else {
		if !strings.HasPrefix(path, "/api/v1/workspaces/") {
			return false
		}
		p = strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		if len(p) < 2 || p[1] != "distribution" {
			return false
		}
		p = append(p[:1], p[2:]...)
	}
	if len(p) < 2 || !wire.ValidID(p[0]) {
		return false
	}
	resource := p[1]
	if resource != "aliases" && resource != "keys" && resource != "requests" && !(admin && resource == "gateways") {
		return false
	}
	if len(p) == 2 {
		return true
	}
	if !wire.ValidID(p[2]) {
		return false
	}
	if len(p) == 3 {
		return true
	}
	if len(p) == 4 {
		return resource == "keys" && p[3] == "revoke" || admin && (resource == "gateways" && p[3] == "revoke" || resource == "aliases" && p[3] == "disable" || resource == "requests" && p[3] == "recoveries")
	}
	if !admin || resource != "requests" || p[3] != "recoveries" || !wire.ValidID(p[4]) {
		return false
	}
	return len(p) == 5 || len(p) == 6 && (p[5] == "fence" || p[5] == "confirm")
}
