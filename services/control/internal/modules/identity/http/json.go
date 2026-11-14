package http

import (
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func decode(r *http.Request, value any) error { return httpserver.DecodeJSON(r, value) }
