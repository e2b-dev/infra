package writer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// test writer that stores the written data
type testWriter struct {
	mu   sync.Mutex
	data []byte
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *testWriter) Data() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.data
}

func TestPostProcessor_Start(t *testing.T) {
	type fields struct {
		testErr       error
		shouldContain string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "test error",
			fields: fields{
				testErr:       errors.New("test error"),
				shouldContain: "Postprocessing failed:",
			},
		},
		{
			name: "test success",
			fields: fields{
				testErr:       nil,
				shouldContain: "Postprocessing finished.",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tw := &testWriter{}
			ctx := context.TODO()
			errChan := make(chan error)

			p := &PostProcessor{
				ctx:     ctx,
				writer:  tw,
				errChan: errChan,
				stopCh:  make(chan struct{}, 1),
				ticker:  time.NewTicker(tickerInterval),
			}

			end := make(chan struct{}, 1)
			go func() {
				p.Start()

				end <- struct{}{}
			}()
			p.Stop(ctx, tt.fields.testErr)

			// Wait for the start goroutine to finish
			<-end

			logs := string(tw.Data())
			if !strings.Contains(logs, tt.fields.shouldContain) {
				t.Errorf("expected data to contain %s, got %s", tt.fields.shouldContain, logs)
			}
		})
	}
}
