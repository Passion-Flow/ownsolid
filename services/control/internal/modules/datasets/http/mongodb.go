package http

import (
	"errors"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func (h *Handler) mongodb(r *http.Request, a identity.Auth, workspace, dataset string, tail []string, requestID string) (any, int, error) {
	if len(tail) == 1 && r.Method == "GET" {
		switch tail[0] {
		case "sources":
			out, e := h.Service.MongoDBSources(r.Context(), a, workspace, dataset)
			return out, 200, e
		case "imports":
			out, e := h.Service.MongoDBImports(r.Context(), a, workspace, dataset)
			return out, 200, e
		}
	}
	if len(tail) == 2 && tail[0] == "imports" && wire.ValidID(tail[1]) && r.Method == "GET" {
		out, e := h.Service.MongoDBGetImport(r.Context(), a, workspace, dataset, tail[1])
		return out, 200, e
	}
	if len(tail) == 1 && r.Method == "POST" && (tail[0] == "preflight" || tail[0] == "imports") {
		var input datasets.MongoDBRequest
		if e := decode(r, &input, httpserver.MaxJSONBytes); e != nil {
			return nil, 0, e
		}
		if tail[0] == "preflight" {
			out, e := h.Service.MongoDBPreflight(r.Context(), a, workspace, dataset, input)
			return out, 200, e
		}
		k, e := key(r)
		if e != nil {
			return nil, 0, e
		}
		out, e := h.Service.MongoDBImport(r.Context(), a, workspace, dataset, input, k, requestID)
		return out, 202, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
