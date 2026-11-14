package http

import (
	"net/http"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

const (
	// sessionPath reports how long the caller's session has left.
	sessionPath = "/api/v1/me/session"
	// backgroundHeader marks a read the person did not ask for, such as a page's poll.
	backgroundHeader = "X-Ownsolid-Background"
)

// backgroundRead keeps a marked read from counting as activity. Only reads qualify: a
// write is someone acting. A client gains nothing by lying: omitting the header keeps its
// own session alive, which any request of its own could do anyway.
func backgroundRead(r *http.Request) *http.Request {
	if r.Method != http.MethodGet || r.Header.Get(backgroundHeader) != "true" {
		return r
	}
	return r.WithContext(identity.WithoutSessionTouch(r.Context()))
}
