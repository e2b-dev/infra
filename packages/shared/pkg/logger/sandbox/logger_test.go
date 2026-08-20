package sbxlogger

import "testing"

func TestExternalLogEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "ClickHouse enabled", enabled: true, want: ClickhouseLogsWriteEndpoint},
		{name: "Loki collector", enabled: false, want: "http://collector/logs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ExternalLogEndpoint("http://collector/logs", tt.enabled); got != tt.want {
				t.Fatalf("ExternalLogEndpoint() = %q, want %q", got, tt.want)
			}
		})
	}
}
