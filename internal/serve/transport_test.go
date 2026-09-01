package serve

import "testing"

// The forward path must not impose its own deadline on how long the other
// side takes to START answering. The CONNECT path never had one, and a
// 60-second ResponseHeaderTimeout here meant the same slow page worked over
// HTTPS but died over plain HTTP (a heavy report behind the corporate proxy
// was the real case). The client owns that decision: its timeout cancels
// req.Context(), which cancels the upstream request.
func TestForwardTransportHasNoResponseHeaderTimeout(t *testing.T) {
	// nil state is fine: transportFor never touches it.
	s := NewServer(nil, discardLogger())
	for _, up := range []Upstream{
		{Kind: KindHTTP, Addr: "proxy.corp:3128"},
		{Kind: KindDirect},
	} {
		tr := s.transportFor(up)
		if tr.ResponseHeaderTimeout != 0 {
			t.Errorf("%s: ResponseHeaderTimeout = %v, esperava 0 (sem limite)", up.Kind, tr.ResponseHeaderTimeout)
		}
	}
}
