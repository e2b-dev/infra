package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestSandboxCreate(t *testing.T) {
	var wg sync.WaitGroup
	// Two tests are run because the first one downloads the template to cache (so it's slow),
	// and the second one uses the cached template (so it should be fast).
	for i := 0; i < 2; i++ {
		wg.Add(1)
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			c := setup.GetAPIClient()

			sbxTimeout := int32(60)
			resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
				TemplateID: setup.SandboxTemplateID,
				Timeout:    &sbxTimeout,
			}, setup.WithAPIKey())

			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("Response: %s", string(resp.Body))
				}
			})

			assert.Equal(t, http.StatusCreated, resp.StatusCode())
		})
		wg.Wait()
	}
}
