package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestTemplateBuild(t *testing.T) {
	if setup.RunBuildTest == "" {
		t.Skip("Build test is disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesTemplateIDBuildsBuildIDWithResponse(ctx, setup.SandboxTemplateID, setup.BuildIDToBeBuild, setup.WithAccessToken())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	fmt.Printf("Resp build test: %s\n", resp.HTTPResponse.Request.URL.String())
	assert.Equal(t, http.StatusAccepted, resp.StatusCode())

	var finished bool
	for !finished {
		resp2, err := c.GetTemplatesTemplateIDBuildsBuildIDStatusWithResponse(ctx, setup.SandboxTemplateID, setup.BuildIDToBeBuild, nil, setup.WithAccessToken())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("Response: %s", string(resp2.Body))
			}
		})

		if resp2.StatusCode() != http.StatusOK {
			t.Fatal("Unexpected status code")
		}

		var statusData api.TemplateBuild
		err = json.Unmarshal(resp2.Body, &statusData)
		if err != nil {
			t.Fatal(err)
		}

		switch statusData.Status {
		case api.TemplateBuildStatusReady:
			finished = true
		case api.TemplateBuildStatusError:
			t.Fatal("Build failed")
		}
	}

	assert.True(t, finished)
}
