package template_manager

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
)

type testStatusClient interface {
	TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error)
}

type testTemplateManagerClient interface {
	SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error
	SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error
}

type fakeStatusClient struct {
	templateBuildStatusResponse *template_manager.TemplateBuildStatusResponse
	err                         error
}

func (f *fakeStatusClient) TemplateBuildStatus(ctx context.Context, in *template_manager.TemplateStatusRequest, opts ...grpc.CallOption) (*template_manager.TemplateBuildStatusResponse, error) {
	return f.templateBuildStatusResponse, f.err
}

type fakeTemplateManagerClient struct {
	setStatusError   error
	setFinishedError error
}

func (f fakeTemplateManagerClient) SetStatus(ctx context.Context, templateID string, buildID uuid.UUID, status envbuild.Status, reason string) error {
	return f.setStatusError
}
func (f fakeTemplateManagerClient) SetFinished(ctx context.Context, templateID string, buildID uuid.UUID, rootfsSize int64, envdVersion string) error {
	return f.setFinishedError
}

func TestPollBuildStatus_setStatus(t *testing.T) {
	type fields struct {
		statusClient testStatusClient
		buildID      uuid.UUID
	}

	tests := []struct {
		name    string
		fields  fields
		wantErr bool
		status  *template_manager.TemplateBuildStatusResponse
		err     error
	}{
		{
			name: "should return error if status is nil",
			fields: fields{
				statusClient: &fakeStatusClient{
					templateBuildStatusResponse: nil,
				},
				buildID: uuid.New(),
			},
			wantErr: true,
		},
		// return context deadline exceeded
		{
			name: "should return context deadline exceeded",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: errors.New("context deadline exceeded"),
				},
			},
			wantErr: true,
			status:  nil,
		},
		// if error is not context deadline exceeded, return error
		{
			name: "should return error if error is not context deadline exceeded",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: errors.New("some other error"),
				},
			},
			wantErr: true,
			status:  nil,
		},
		// if status client and err are nil, return err
		{
			name: "should return error if status client and err are nil",
			fields: fields{
				statusClient: nil,
			},
			wantErr: true,
		},
		// if status client present and err is nil, return nil
		{
			name: "should return nil if status client present and err is nil",
			fields: fields{
				statusClient: &fakeStatusClient{
					err: nil,
					templateBuildStatusResponse: &template_manager.TemplateBuildStatusResponse{
						Status: template_manager.TemplateBuildState_Completed,
					},
				},
			},
			wantErr: false,
			status: &template_manager.TemplateBuildStatusResponse{
				Status: template_manager.TemplateBuildState_Completed,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PollBuildStatus{
				statusClient: tt.fields.statusClient,
				logger:       zap.NewNop(),
			}
			err := c.setStatus(context.TODO())
			if tt.wantErr {
				if err == nil {
					t.Errorf("PollBuildStatus.getSetStatusFn() = %v", err)
				}
			}
			if !tt.wantErr {
				if err != nil {
					t.Errorf("PollBuildStatus.getSetStatusFn() = %v", err)
				}
			}
			if tt.status.GetStatus() != c.status.GetStatus() {
				t.Errorf("PollBuildStatus.getSetStatusFn() = %v, want %v", c.status, tt.status)
			}
		})
	}
}

func TestPollBuildStatus_dispatchBasedOnStatus(t *testing.T) {
	type fields struct {
		templateManagerClient *fakeTemplateManagerClient
	}
	type args struct {
		status *template_manager.TemplateBuildStatusResponse
	}
	tests := []struct {
		name              string
		fields            fields
		args              args
		wantCompleteState bool
		wantErr           bool
	}{
		{
			name: "should return error if status is nil",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: nil,
			},
			wantCompleteState: false,
			wantErr:           true,
		},
		{
			name: "should handle failed status",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{
					setStatusError: errors.New("failed to set status"),
				},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Failed,
				},
			},
			wantCompleteState: false,
			wantErr:           true,
		},
		{
			name: "should handle completed status with nil metadata",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
				},
			},
			wantCompleteState: false,
			wantErr:           true,
		},
		{
			name: "should handle completed status successfully",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{
					setFinishedError: errors.New("failed to set finished"),
				},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
					Metadata: &template_manager.TemplateBuildMetadata{
						RootfsSizeKey:  100,
						EnvdVersionKey: "1.0.0",
					},
				},
			},
			wantCompleteState: false,
			wantErr:           true,
		},
		{
			name: "should not send to done channel for building status",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Building,
				},
			},
			wantCompleteState: false,
			wantErr:           false,
		},
		// should not get error when no error setting status
		{
			name: "should not get error when no error setting status",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Building,
				},
			},
			wantErr:           false,
			wantCompleteState: false,
		},
		// should not get error when no error setting finished
		{
			name: "should not get error when no error setting finished",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
					Metadata: &template_manager.TemplateBuildMetadata{
						RootfsSizeKey:  100,
						EnvdVersionKey: "1.0.0",
					},
				},
			},
			wantErr:           false,
			wantCompleteState: true,
		},
		// should error when nil metadata
		{
			name: "should error when nil metadata",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Completed,
				},
			},
			wantErr:           true,
			wantCompleteState: false,
		},
		// should not error when status is failure
		{
			name: "should not error when status is failure",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: &template_manager.TemplateBuildStatusResponse{
					Status: template_manager.TemplateBuildState_Failed,
				},
			},

			wantErr:           false,
			wantCompleteState: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PollBuildStatus{
				templateManagerClient: tt.fields.templateManagerClient,
				logger:                zap.NewNop(),
			}

			err, completed := c.dispatchBasedOnStatus(context.TODO(), tt.args.status)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error, got no error")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
			}

			if tt.wantCompleteState {
				if !completed {
					t.Errorf("Expected completed, got failure")
				}
			} else {
				if completed {
					t.Errorf("Expected failure, got completed")
				}
			}
		})
	}
}
