//go:build linux

package fc

import (
	"encoding/json"
	"testing"
)

func TestNetworkMetricsEvidencePresence(t *testing.T) {
	var partial firecrackerMetrics
	if err := json.Unmarshal([]byte(`{"net":{"tx_bytes_count":17}}`), &partial); err != nil {
		t.Fatal(err)
	}
	if tx, rx, present := partial.Net.byteDeltas(); tx != 17 || rx != 0 || present {
		t.Fatal("partial observability changed or incomplete evidence accepted")
	}
	for _, input := range []string{`{}`, `{"net":null}`, `{"net":{}}`, `{"net":{"tx_bytes_count":0}}`, `{"net":{"tx_bytes_count":0,"rx_bytes_count":null}}`} {
		var m firecrackerMetrics
		if err := json.Unmarshal([]byte(input), &m); err != nil {
			t.Fatal(err)
		}
		if _, _, present := m.Net.byteDeltas(); present {
			t.Fatalf("unknown counters became zero evidence: %s", input)
		}
	}
	for _, input := range []string{`{"net":{"tx_bytes_count":-1,"rx_bytes_count":0}}`, `{"net":{"tx_bytes_count":18446744073709551616,"rx_bytes_count":0}}`, `{"net":{"tx_bytes_count":1.5,"rx_bytes_count":0}}`, `{"net":`} {
		var m firecrackerMetrics
		if json.Unmarshal([]byte(input), &m) == nil {
			t.Fatalf("malformed counters accepted: %s", input)
		}
	}
	for _, tc := range []struct {
		input  string
		tx, rx uint64
	}{
		{`{"net":{"tx_bytes_count":0,"rx_bytes_count":0}}`, 0, 0},
		{`{"net":{"tx_bytes_count":9007199254740993,"rx_bytes_count":18446744073709551615}}`, 9007199254740993, 18446744073709551615},
	} {
		var m firecrackerMetrics
		if err := json.Unmarshal([]byte(tc.input), &m); err != nil {
			t.Fatal(err)
		}
		tx, rx, present := m.Net.byteDeltas()
		if !present || tx != tc.tx || rx != tc.rx {
			t.Fatalf("precision/presence lost: %d %d %t", tx, rx, present)
		}
	}
}
