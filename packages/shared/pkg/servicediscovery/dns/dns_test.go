package dns

import (
	"net"
	"testing"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

// stubResolver answers every query with the given rcode and answers.
func stubResolver(t *testing.T, rcode int, answers []string) string {
	t.Helper()

	conn, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	require.NoError(t, err)

	mux := dns.NewServeMux()
	mux.HandleFunc(".", func(w dns.ResponseWriter, req *dns.Msg) {
		reply := new(dns.Msg).SetRcode(req, rcode)
		for _, ip := range answers {
			rr, rrErr := dns.NewRR(req.Question[0].Name + " 1 IN A " + ip)
			if rrErr != nil {
				t.Errorf("building answer: %v", rrErr)

				return
			}
			reply.Answer = append(reply.Answer, rr)
		}
		if writeErr := w.WriteMsg(reply); writeErr != nil {
			t.Errorf("writing reply: %v", writeErr)
		}
	})

	server := &dns.Server{PacketConn: conn, Handler: mux}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	return conn.LocalAddr().String()
}

// A resolver outage answers SERVFAIL with no error and no records. Reported as
// success it reads as "this service has no instances", and the caller replaces
// its last known set with nothing.
func TestDNS_AFailureRcodeIsReportedAsAnError(t *testing.T) {
	t.Parallel()

	d := New([]string{"orchestrator.service"}, stubResolver(t, dns.RcodeServerFailure, nil), 5008)

	instances, err := d.ListInstances(t.Context())
	require.Error(t, err, "a SERVFAIL must not read as an empty fleet")
	assert.Contains(t, err.Error(), "SERVFAIL")
	assert.Empty(t, instances)
}

// NOERROR with an empty answer section genuinely means no records, and stays a
// successful empty result.
func TestDNS_NoRecordsIsASuccessfulEmptySet(t *testing.T) {
	t.Parallel()

	d := New([]string{"orchestrator.service"}, stubResolver(t, dns.RcodeSuccess, nil), 5008)

	instances, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	assert.Empty(t, instances)
}

func TestDNS_ResolvedAddressesBecomeInstances(t *testing.T) {
	t.Parallel()

	d := New([]string{"orchestrator.service"}, stubResolver(t, dns.RcodeSuccess, []string{"10.0.0.1"}), 5008)

	instances, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "10.0.0.1:5008", instances[0].Address())
	assert.Equal(t, servicediscovery.BackendDNS, instances[0].Backend)
}
