// Package http serves the capability declaration to anyone signed in, in either entrance.
package http

import (
	"errors"
	"net/http"

	"github.com/Passion-Flow/ownsolid/pkg/capabilities"
)

const path = "/api/v1/capabilities"

// Response is the whole declaration, in display order. It holds no tenant data and does not
// change while a process runs, so it has no paging or filters.
type Response struct {
	Registries []capabilities.Registry `json:"registries"`
}

// Owns reports the paths this handler serves.
func Owns(requestPath string) bool { return requestPath == path }

// Handle answers a request the dispatcher has already authenticated.
func Handle(r *http.Request) (any, int, error) {
	if !Owns(r.URL.Path) || r.URL.RawPath != "" || r.Method != http.MethodGet {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if r.URL.RawQuery != "" {
		return nil, 0, errors.New("VALIDATION_FAILED")
	}
	return Response{Registries: capabilities.Registries()}, http.StatusOK, nil
}
