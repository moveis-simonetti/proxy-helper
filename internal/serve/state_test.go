package serve

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

// withConfigDir points os.UserConfigDir at a temp dir so tests never touch
// the developer's real profiles.
//
// It isolates XDG_RUNTIME_DIR too, and that is not incidental: State.Reload
// and the prober both publish the runtime state file, so any test that
// builds a State and reloads it would otherwise write a bogus state.json
// into the developer's live session — which the GUI and "proxy status" then
// read as if a real daemon had published it. Every test in this package that
// touches State must go through here.
func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	return filepath.Join(dir, "proxy-helper", "config.json")
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// stateFrom wraps a router in a State, for tests that exercise the server
// without going through a config file.
func stateFrom(r Router) *State {
	s := &State{logger: NewLogger(io.Discard, false)}
	s.current.Store(&snapshot{router: r, summary: "test"})
	return s
}

// autoState builds a State in mode auto with the given verdict and a live
// kick channel, for exercising NoteUpstreamFailure without a real prober.
func autoState(t *testing.T, reachable bool) *State {
	t.Helper()
	s := &State{logger: NewLogger(io.Discard, false), kick: make(chan struct{}, 1)}
	s.current.Store(&snapshot{router: directRouter{}, summary: "test", mode: proxy.ModeAuto})
	s.reachable.Store(reachable)
	return s
}

func TestNoteUpstreamFailureWakesOnDialErrorInAuto(t *testing.T) {
	s := autoState(t, true)
	dialErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	s.NoteUpstreamFailure(Upstream{Kind: KindHTTP, Addr: "proxy.example:8080"}, dialErr)

	select {
	case <-s.kick:
	default:
		t.Fatal("expected NoteUpstreamFailure to wake the prober on a dial error")
	}
}

func TestNoteUpstreamFailureIgnoresDirectRequests(t *testing.T) {
	s := autoState(t, true)
	dialErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	s.NoteUpstreamFailure(Upstream{Kind: KindDirect}, dialErr)

	select {
	case <-s.kick:
		t.Fatal("a direct-request failure says nothing about the upstream and must not wake the prober")
	default:
	}
}

func TestNoteUpstreamFailureIgnoresNonDialErrors(t *testing.T) {
	s := autoState(t, true)
	timeoutErr := &net.OpError{Op: "read", Err: errors.New("i/o timeout")}

	s.NoteUpstreamFailure(Upstream{Kind: KindHTTP, Addr: "proxy.example:8080"}, timeoutErr)

	select {
	case <-s.kick:
		t.Fatal("a post-dial error (e.g. a slow origin) must not wake the prober")
	default:
	}
}

func TestNoteUpstreamFailureIgnoresAlreadyDownVerdict(t *testing.T) {
	s := autoState(t, false)
	dialErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}

	s.NoteUpstreamFailure(Upstream{Kind: KindHTTP, Addr: "proxy.example:8080"}, dialErr)

	select {
	case <-s.kick:
		t.Fatal("the prober is already probing at the fast interval once the verdict is down; no need to wake it")
	default:
	}
}

func TestStateReloadPicksUpNewProfile(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"},
		"b":{"scheme":"http","host":"b.corp","port":"3128"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := st.Router().Route("example.com").Addr; got != "a.corp:8080" {
		t.Fatalf("initial upstream = %q, want a.corp:8080", got)
	}

	writeConfig(t, path, `{"active_profile":"b","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"},
		"b":{"scheme":"http","host":"b.corp","port":"3128"}}}`)
	if err := st.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := st.Router().Route("example.com").Addr; got != "b.corp:3128" {
		t.Errorf("after reload upstream = %q, want b.corp:3128", got)
	}
}

func TestStateReloadKeepsPreviousStateOnBrokenConfig(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	writeConfig(t, path, `{ this is not json`)
	if err := st.Reload(); err == nil {
		t.Fatal("Reload should report an error for broken JSON")
	}
	if got := st.Router().Route("example.com").Addr; got != "a.corp:8080" {
		t.Errorf("broken reload must preserve the previous state, got %q", got)
	}
}

func TestStateEmptyActiveProfileIsAllDirect(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := st.Router().Route("example.com").Kind; got != KindDirect {
		t.Errorf("no active profile must route DIRECT, got %v", got)
	}
}

func TestStateMergesGlobalNoProxy(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","global_no_proxy":["internal.example"],
		"profiles":{"a":{"scheme":"http","host":"a.corp","port":"8080","no_proxy":["other.example"]}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	for _, host := range []string{"internal.example", "other.example"} {
		if got := st.Router().Route(host).Kind; got != KindDirect {
			t.Errorf("Route(%q) = %v, want KindDirect", host, got)
		}
	}
}

// swapDockerNetworkSubnets replaces the package-level Docker subnet lookup
// for the duration of the test, so loadSnapshot's docker_bridge branch can
// be exercised without depending on this host's real network interfaces.
func swapDockerNetworkSubnets(t *testing.T, fn func() ([]string, error)) {
	t.Helper()
	orig := lookupDockerNetworkSubnets
	lookupDockerNetworkSubnets = fn
	t.Cleanup(func() { lookupDockerNetworkSubnets = orig })
}

func TestDockerBridgeAddsDetectedSubnetsToNoProxy(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","docker_bridge":true,
		"profiles":{"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)
	swapDockerNetworkSubnets(t, func() ([]string, error) { return []string{"172.19.0.0/16"}, nil })

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := st.Router().Route("172.19.0.5").Kind; got != KindDirect {
		t.Errorf("Route(172.19.0.5) = %v, want DIRECT once its network is detected as a Docker bridge", got)
	}
	// Unrelated traffic must still go through the proxy — this is an
	// addition to the no-proxy list, not a switch to direct-everything.
	if got := st.Router().Route("example.com").Kind; got != KindHTTP {
		t.Errorf("Route(example.com) = %v, want the profile's upstream, untouched by the docker exclusion", got)
	}
}

func TestNoDockerBridgeSkipsSubnetDetection(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a",
		"profiles":{"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)
	called := false
	swapDockerNetworkSubnets(t, func() ([]string, error) {
		called = true
		return []string{"172.19.0.0/16"}, nil
	})

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if called {
		t.Error("subnet detection must not run when docker_bridge is off")
	}
	if got := st.Router().Route("172.19.0.5").Kind; got != KindHTTP {
		t.Errorf("Route(172.19.0.5) = %v, docker_bridge is off so nothing should have excluded it", got)
	}
}

// A detection failure (e.g. a permissions error listing interfaces) must
// not take the daemon down: it only means this pass has no extra
// exclusions to add.
func TestDockerSubnetDetectionFailureIsNonFatal(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","docker_bridge":true,
		"profiles":{"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)
	swapDockerNetworkSubnets(t, func() ([]string, error) { return nil, errors.New("boom") })

	if _, err := NewState(NewLogger(io.Discard, false)); err != nil {
		t.Fatalf("NewState must survive a subnet detection failure, got: %v", err)
	}
}

// TestStateSummaryIgnoresNoProxyMatches guards the health line: it must be
// derived from the configured upstream, not from probing a made-up host that
// a no-proxy entry might happen to match.
func TestStateSummaryIgnoresNoProxyMatches(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"a","global_no_proxy":[".invalid"],
		"profiles":{"a":{"scheme":"http","host":"a.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := st.Describe(); !strings.Contains(got, "a.corp:8080") {
		t.Errorf("Describe() = %q, want it to name the configured upstream", got)
	}
}
