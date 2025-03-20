package template_manager

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func TestBuildStatusChecker_run(t *testing.T) {
	type fields struct {
		retries               int
		retryInterval         time.Duration
		tickChannel           chan struct{}
		ctx                   context.Context
		statusClient          statusClient
		logger                *zap.Logger
		templateID            string
		buildID               uuid.UUID
		templateManagerClient templateManagerClient
	}
	tests := []struct {
		name   string
		fields fields
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &BuildStatusChecker{
				retries:               tt.fields.retries,
				retryInterval:         tt.fields.retryInterval,
				tickChannel:           tt.fields.tickChannel,
				ctx:                   tt.fields.ctx,
				statusClient:          tt.fields.statusClient,
				logger:                tt.fields.logger,
				templateID:            tt.fields.templateID,
				buildID:               tt.fields.buildID,
				templateManagerClient: tt.fields.templateManagerClient,
			}
			c.run()
		})
	}
}
