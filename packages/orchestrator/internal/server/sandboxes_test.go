package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/db"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	startTime = time.Now().UTC()
	endTime   = startTime.Add(time.Hour)
)

func Test_server_List(t *testing.T) {
	type args struct {
		ctx context.Context
		in1 *emptypb.Empty
	}
	tests := []struct {
		name    string
		args    args
		want    *orchestrator.SandboxListResponse
		wantErr bool
		data    []*sandbox.Sandbox
	}{
		{
			name: "should return all sandboxes",

			args: args{
				ctx: context.Background(),
				in1: &emptypb.Empty{},
			},
			data: []*sandbox.Sandbox{
				{
					Config: &orchestrator.SandboxConfig{
						TemplateId: "template-id",
					},
					StartedAt: startTime,
					EndAt:     endTime,
				},
			},
			want: &orchestrator.SandboxListResponse{
				Sandboxes: []*orchestrator.RunningSandbox{
					{
						Config: &orchestrator.SandboxConfig{TemplateId: "template-id"},
						// ClientId:  "client-id",
						StartTime: timestamppb.New(startTime),
						EndTime:   timestamppb.New(endTime),
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{
				sandboxes: smap.New[*sandbox.Sandbox](),
				tracer:    noop.NewTracerProvider().Tracer(""),
			}
			for _, sbx := range tt.data {
				s.sandboxes.Insert(sbx.Config.SandboxId, sbx)
			}
			got, err := s.List(tt.args.ctx, tt.args.in1)
			if (err != nil) != tt.wantErr {
				t.Errorf("server.List() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("server.List() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDatabaseSmokeTest(t *testing.T) {
	ctx := t.Context()

	schemaPath, err := filepath.Abs("../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}

	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), fmt.Sprintf("%s.%d.%d.db", t.Name(), time.Now().UTC().Unix(), os.Getpid()))
	drv, err := newDatabaseDriver(path)
	if err != nil {
		t.Fatal(err)
	}

	srvDB, err := db.New(ctx, drv, string(schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := srvDB.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})

	status, err := srvDB.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, int64(1), status.GlobalVersion)
	assert.Equal(t, int64(0), status.NumSandboxes)
	assert.Equal(t, int64(0), status.RunningSandboxes)

	srv := &server{
		sandboxes: smap.New[*sandbox.Sandbox](),
		db:        srvDB,
	}

	id := uuid.New()
	sbxOriginal := &sandbox.Sandbox{
		Config: &orchestrator.SandboxConfig{SandboxId: id.String()},
		EndAt:  time.Now().UTC().Add(time.Hour),
	}

	started, err := srv.recordSandboxStart(ctx, sbxOriginal)
	if err != nil {
		t.Fatal(err)
	}

	assert.True(t, started.Before(sbxOriginal.EndAt))

	status, err = srvDB.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, int64(2), status.GlobalVersion)
	assert.Equal(t, int64(1), status.NumSandboxes)
	assert.Equal(t, int64(1), status.RunningSandboxes)
	assert.Equal(t,
		started.Truncate(time.Millisecond),
		status.OldestSandboxStartTime().Truncate(time.Millisecond),
	)
}
