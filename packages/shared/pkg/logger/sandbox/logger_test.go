package sbxlogger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/launchdarkly/go-server-sdk/v7/testhelpers/ldtestdata"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

func TestNewLoggerExternalDualWriteFlag(t *testing.T) {
	t.Parallel()

	const (
		serviceName = "orchestrator"
		teamID      = "team-123"
		sandboxID   = "sandbox-123"
		templateID  = "template-123"
		message     = "sandbox started"
	)

	tests := []struct {
		name            string
		dualWrite       bool
		wantOTelRecords int
	}{
		{name: "disabled writes only HTTP", dualWrite: false, wantOTelRecords: 0},
		{name: "enabled writes HTTP and OTLP", dualWrite: true, wantOTelRecords: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporter, provider := newTestLoggerProvider(t)
			var httpCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				httpCalls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)

			source := ldtestdata.DataSource()
			featureFlags, err := featureflags.NewClientWithDatasource(source)
			if err != nil {
				t.Fatalf("creating feature flags client: %v", err)
			}
			t.Cleanup(func() {
				if err := featureFlags.Close(t.Context()); err != nil {
					t.Errorf("closing feature flags client: %v", err)
				}
			})
			source.Update(source.Flag(featureflags.LogsDualWriteFlag.Key()).VariationForAll(tt.dualWrite))

			lg := NewLogger(t.Context(), provider, SandboxLoggerConfig{
				ServiceName:      serviceName,
				IsInternal:       false,
				CollectorAddress: server.URL,
				FeatureFlags:     featureFlags,
			})
			lg.With(SandboxMetadata{
				TeamID:     teamID,
				SandboxID:  sandboxID,
				TemplateID: templateID,
			}.Fields()...).Info(t.Context(), message)
			if err := lg.Sync(); err != nil {
				t.Fatalf("syncing logger: %v", err)
			}

			if got := httpCalls.Load(); got != 1 {
				t.Errorf("HTTP collector calls = %d, want 1", got)
			}
			records := exporter.Records()
			if got := len(records); got != tt.wantOTelRecords {
				t.Fatalf("exported OTel records = %d, want %d", got, tt.wantOTelRecords)
			}
			if !tt.dualWrite {
				return
			}

			record := records[0]
			attributes := recordAttributes(record)
			assertStringAttribute(t, attributes, "service", serviceName)
			assertBoolAttribute(t, attributes, "internal", false)
			assertStringAttribute(t, attributes, "team.id", teamID)
			assertStringAttribute(t, attributes, "sandbox.id", sandboxID)
			assertStringAttribute(t, attributes, "template.id", templateID)
			if got := record.Body().AsString(); got != message {
				t.Errorf("body = %q, want %q", got, message)
			}
			if got := record.Severity(); got != log.SeverityInfo {
				t.Errorf("severity = %v, want %v", got, log.SeverityInfo)
			}
		})
	}
}

func newTestLoggerProvider(t *testing.T) (*memoryExporter, *sdklog.LoggerProvider) {
	t.Helper()

	exporter := newMemoryExporter()
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exporter)),
	)
	t.Cleanup(func() {
		if err := provider.Shutdown(t.Context()); err != nil {
			t.Errorf("shutting down logger provider: %v", err)
		}
	})

	return exporter, provider
}

type memoryExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func newMemoryExporter() *memoryExporter {
	return &memoryExporter{}
}

func (e *memoryExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for i := range records {
		e.records = append(e.records, records[i].Clone())
	}

	return nil
}

func (e *memoryExporter) Shutdown(context.Context) error {
	return nil
}

func (e *memoryExporter) ForceFlush(context.Context) error {
	return nil
}

func (e *memoryExporter) Records() []sdklog.Record {
	e.mu.Lock()
	defer e.mu.Unlock()

	return append([]sdklog.Record(nil), e.records...)
}

func recordAttributes(record sdklog.Record) map[string]log.Value {
	attributes := make(map[string]log.Value, record.AttributesLen())
	record.WalkAttributes(func(attribute log.KeyValue) bool {
		attributes[attribute.Key] = attribute.Value

		return true
	})

	return attributes
}

func assertStringAttribute(t *testing.T, attributes map[string]log.Value, key, want string) {
	t.Helper()

	value, ok := attributes[key]
	if !ok {
		t.Errorf("attribute %q is missing", key)

		return
	}
	if got := value.AsString(); got != want {
		t.Errorf("attribute %q = %q, want %q", key, got, want)
	}
}

func assertBoolAttribute(t *testing.T, attributes map[string]log.Value, key string, want bool) {
	t.Helper()

	value, ok := attributes[key]
	if !ok {
		t.Errorf("attribute %q is missing", key)

		return
	}
	if got := value.AsBool(); got != want {
		t.Errorf("attribute %q = %t, want %t", key, got, want)
	}
}
