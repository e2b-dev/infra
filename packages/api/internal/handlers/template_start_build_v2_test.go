package handlers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
)

func TestUserAgentToTemplateVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{
			name:      "current JS SDK",
			userAgent: "e2b-js-sdk/2.31.0",
			want:      templates.TemplateV2LatestVersion,
		},
		{
			name:      "current JS SDK with CLI integration",
			userAgent: "e2b-js-sdk/v2.31.0 e2b-cli/2.13.0",
			want:      templates.TemplateV2LatestVersion,
		},
		{
			name:      "old JS SDK with CLI integration",
			userAgent: "e2b-js-sdk/2.2.0 e2b-cli/2.13.0",
			want:      templates.TemplateV2BetaVersion,
		},
		{
			name:      "current Python SDK with integration",
			userAgent: "e2b-python-sdk/2.31.0 custom-client/1.0.0",
			want:      templates.TemplateV2LatestVersion,
		},
		{
			name:      "unrecognized user agent",
			userAgent: "custom-client/1.0.0",
			want:      templates.TemplateV2LatestVersion,
		},
		{
			name:      "empty user agent",
			userAgent: "",
			want:      templates.TemplateV2LatestVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := userAgentToTemplateVersion(context.Background(), logger.L(), tt.userAgent)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
