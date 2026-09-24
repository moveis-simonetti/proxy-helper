package serve

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// listenOn opens a real listener on an ephemeral port of host and returns
// its address. Probing is about observed reality, so the tests use real
// sockets rather than a fake dialer.
func listenOn(t *testing.T, host string) (string, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("listening on %s: %v", host, err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), ln
}

func TestProbeListeningReportsOnlyLiveAddrs(t *testing.T) {
	live, _ := listenOn(t, "127.0.0.1")

	// A closed listener's address is a port nothing is bound to anymore,
	// which is exactly the "configured but not actually listening" case
	// this whole change exists to catch.
	dead, deadLn := listenOn(t, "127.0.0.1")
	deadLn.Close()

	got := ProbeListening([]string{live, dead}, 500*time.Millisecond)
	if len(got) != 1 || got[0] != live {
		t.Errorf("ProbeListening = %v, want [%s]", got, live)
	}
}

func TestProbeListeningPreservesOrder(t *testing.T) {
	first, _ := listenOn(t, "127.0.0.1")
	second, _ := listenOn(t, "127.0.0.1")

	got := ProbeListening([]string{first, second}, 500*time.Millisecond)
	want := []string{first, second}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ProbeListening = %v, want %v", got, want)
	}
}

func TestProbeListeningEmptyWhenNothingIsUp(t *testing.T) {
	dead, ln := listenOn(t, "127.0.0.1")
	ln.Close()

	if got := ProbeListening([]string{dead}, 500*time.Millisecond); len(got) != 0 {
		t.Errorf("ProbeListening = %v, want empty", got)
	}
}
