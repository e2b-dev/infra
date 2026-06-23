//go:build !e2bfragment

package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

// RegisterDebugRoutes is a no-op in production builds. The debug fragmenter is
// only compiled with `-tags e2bfragment` (see fragment.go).
func RegisterDebugRoutes(_ chi.Router, _ *zerolog.Logger) {}
