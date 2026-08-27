package serve

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"proxy-helper/internal/proxy"
)

// These run against real listeners rather than a stubbed transport: what is
// being tested is how Go's http client reports each failure, and a stub
// would only confirm the assumptions this code is meant to verify.

// fakeProxy starts an HTTP proxy that answers every absolute-URL request
// with the given status, and returns the config pointing at it.
func fakeProxy(t *testing.T, status int) proxy.Config {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status == http.StatusProxyAuthRequired {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	return proxy.Config{Scheme: "http", Host: u.Hostname(), Port: u.Port()}
}

func TestProbeAcceptsWorkingCredentials(t *testing.T) {
	cfg := fakeProxy(t, http.StatusOK)

	got, err := Probe(context.Background(), cfg, "gestao", "senha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ProbeOK {
		t.Errorf("result = %v, want ProbeOK", got)
	}
}

func TestProbeTreatsANonAuthStatusAsSuccess(t *testing.T) {
	// A 404 comes from the origin server, which means the proxy passed the
	// request through — the credentials worked. Reporting this as a failure
	// would send people chasing a password that is already correct.
	cfg := fakeProxy(t, http.StatusNotFound)

	got, err := Probe(context.Background(), cfg, "gestao", "senha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ProbeOK {
		t.Errorf("result = %v, want ProbeOK for a 404 from the origin", got)
	}
}

func TestProbeDetectsRefusedCredentials(t *testing.T) {
	cfg := fakeProxy(t, http.StatusProxyAuthRequired)

	got, err := Probe(context.Background(), cfg, "gestao", "senha-errada")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ProbeBadCredentials {
		t.Errorf("result = %v, want ProbeBadCredentials", got)
	}
}

func TestProbeDetectsAnUnknownAddress(t *testing.T) {
	// The distinction that matters: this one is the user's to fix.
	cfg := proxy.Config{
		Scheme: "http",
		Host:   "proxy-que-nao-existe.invalid",
		Port:   "3128",
	}

	got, _ := Probe(context.Background(), cfg, "gestao", "senha")
	if got != ProbeUnknownHost {
		t.Errorf("result = %v, want ProbeUnknownHost — a typo must not read as an outage", got)
	}
}

func TestProbeDetectsAnAddressThatDoesNotAnswer(t *testing.T) {
	// A port nobody listens on: resolves fine, refuses the connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close() // free it, so connecting is refused

	cfg := proxy.Config{Scheme: "http", Host: "127.0.0.1", Port: strconv.Itoa(addr.Port)}

	got, _ := Probe(context.Background(), cfg, "gestao", "senha")
	if got != ProbeUnreachable {
		t.Errorf("result = %v, want ProbeUnreachable", got)
	}
}

func TestProbeStopsWhenTheContextIsCancelled(t *testing.T) {
	cfg := fakeProxy(t, http.StatusOK)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A cancelled probe must not report success: the window's Cancel button
	// depends on this, and a probe that ignored it would save credentials
	// nobody confirmed.
	if got, _ := Probe(ctx, cfg, "gestao", "senha"); got == ProbeOK {
		t.Error("a cancelled probe reported ProbeOK")
	}
}

func TestClassifyProbeErrorReadsA407FromATunnelFailure(t *testing.T) {
	// CONNECT refusals never become a *http.Response, so the status only
	// exists in the error text. This is the fragile path and the reason it
	// has its own test.
	err := &url.Error{
		Op:  "Get",
		URL: probeURL,
		Err: errProxyConnect{},
	}
	if got := classifyProbeError(err); got != ProbeBadCredentials {
		t.Errorf("result = %v, want ProbeBadCredentials for %v", got, err)
	}
}

type errProxyConnect struct{}

func (errProxyConnect) Error() string {
	return `proxyconnect tcp: Proxy Authentication Required`
}
