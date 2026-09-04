package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoLogStoreError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lokiURL    string
		clickhouse string
		wantErr    bool
	}{
		{name: "Loki only", lokiURL: "http://loki:3100"},
		{name: "ClickHouse only", clickhouse: "clickhouse://clickhouse:9000/default"},
		{name: "both", lokiURL: "http://loki:3100", clickhouse: "clickhouse://clickhouse:9000/default"},
		{name: "neither is refused", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := noLogStoreError(tt.lokiURL, tt.clickhouse)
			if !tt.wantErr {
				require.NoError(t, err)

				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "LOKI_URL")
			assert.Contains(t, err.Error(), "CLICKHOUSE_CONNECTION_STRING")
		})
	}
}
