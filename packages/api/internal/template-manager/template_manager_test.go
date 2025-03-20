package template_manager

import (
	"context"
	"testing"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
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

func TestPollBuildStatus_getFuncToRetry(t *testing.T) {
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
			}
			var status *template_manager.TemplateBuildStatusResponse
			retryFunc := c.getFuncToRetry(status)
			err := retryFunc()
			if tt.wantErr {
				if err == nil {
					t.Errorf("PollBuildStatus.getFuncToRetry() = %v", err)
				}
			}
			if !tt.wantErr {
				if err != nil {
					t.Errorf("PollBuildStatus.getFuncToRetry() = %v", err)
				}
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
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "should return error if status is nil",
			fields: fields{
				templateManagerClient: &fakeTemplateManagerClient{},
			},
			args: args{
				status: nil,
			},
			wantErr: true,
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
			wantErr: true,
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
			wantErr: true,
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
			wantErr: true,
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
			wantErr: false,
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
			wantErr: false,
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
			wantErr: false,
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
			wantErr: true,
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
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PollBuildStatus{
				templateManagerClient: tt.fields.templateManagerClient,
			}

			err := c.dispatchBasedOnStatus(tt.args.status)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error, got no error")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
			}
		})
	}
}

type fakeBuildStatusSetter struct {
	setBuildStatusError error
}

func (f *fakeBuildStatusSetter) setBuildStatus() error {
	return f.setBuildStatusError
}

func TestPollBuildStatus_getPollRetryFunction(t *testing.T) {
	type fields struct {
		ctx context.Context
	}

	type args struct {
		bss *fakeBuildStatusSetter
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{

		// return error if context is nil
		{
			name: "should return error if context is nil",
			args: args{
				bss: &fakeBuildStatusSetter{
					setBuildStatusError: nil,
				},
			},
			fields: fields{
				ctx: nil,
			},
			wantErr: true,
		},
		// return nil if context is not nil and bss returns nil
		{
			name: "should return nil if context is not nil and bss returns nil",
			args: args{
				bss: &fakeBuildStatusSetter{
					setBuildStatusError: nil,
				},
			},
			fields: fields{
				ctx: context.Background(),
			},
			wantErr: false,
		},
		// return error if context is not nil and bss returns error
		{
			name: "should return error if context is not nil and bss returns error",
			args: args{
				bss: &fakeBuildStatusSetter{
					setBuildStatusError: errors.New("failed to set build status"),
				},
			},
			fields: fields{
				ctx: context.Background(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &PollBuildStatus{
				ctx: tt.fields.ctx,
			}
			f := c.getPollRetryFunction(tt.args.bss)
			err := f()
			if (err != nil) != tt.wantErr {
				t.Errorf("getPollRetryFunction() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
