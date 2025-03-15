package templates

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

func TestTemplateRequestBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(ctx, api.TemplateBuildRequest{
		Dockerfile: "FROM alpine:3.14\nRUN echo 'Hello, World!'",
	}, setup.WithAccessToken())

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	assert.Equal(t, http.StatusAccepted, resp.StatusCode())

	var data api.Template
	err = json.Unmarshal(resp.Body, &data)
	if err != nil {
		t.Fatal(err)
	}

	resp2, err := c.GetTemplatesTemplateIDBuildsBuildIDStatusWithResponse(ctx, data.TemplateID, data.BuildID, nil, setup.WithAccessToken())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp2.Body))
		}
	})

	assert.Equal(t, http.StatusOK, resp2.StatusCode())
	var statusData api.TemplateBuild
	err = json.Unmarshal(resp2.Body, &statusData)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, api.TemplateBuildStatusWaiting, statusData.Status)
}
