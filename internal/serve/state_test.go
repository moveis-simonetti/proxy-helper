package serve

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withConfigDir points os.UserConfigDir at a temp dir so tests never touch
// the developer's real profiles.
func withConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
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
