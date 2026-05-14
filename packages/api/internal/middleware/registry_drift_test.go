package middleware

import (
	"sort"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

type registryNoopHandlers struct{ api.ServerInterface }

func TestDefaultRouteIntentsCoversAllRegisteredRoutes(t *testing.T) {
	t.Parallel()

	r := gin.New()
	api.RegisterHandlersWithOptions(r, registryNoopHandlers{}, api.GinServerOptions{})

	declared := map[string]struct{}{}
	for method, paths := range DefaultRouteIntents {
		for path := range paths {
			declared[method+" "+path] = struct{}{}
		}
	}

	registered := map[string]struct{}{}
	var missing, extra []string
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		registered[key] = struct{}{}
		if _, exempt := IntentExemptRoutes[route.Path]; exempt {
			continue
		}
		if _, ok := declared[key]; !ok {
			missing = append(missing, key)
		}
	}
	for key := range declared {
		if _, ok := registered[key]; !ok {
			extra = append(extra, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("MISSING from registry:\n  %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("EXTRA in registry:\n  %v", extra)
	}
}
