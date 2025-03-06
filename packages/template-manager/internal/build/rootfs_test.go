package build

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
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
			}
			go p.Start()
			p.stop(tt.fields.testErr)
			close(errChan)

			if !strings.Contains(string(tw.data), tt.fields.shouldContain) {
				t.Errorf("expected data to contain %s, got %s", tt.fields.shouldContain, string(tw.data))
			}

		})
	}
}
