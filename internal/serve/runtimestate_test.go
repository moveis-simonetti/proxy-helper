package serve

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"proxy-helper/internal/proxy"
)

// runtimeDir returns the isolated XDG_RUNTIME_DIR that withConfigDir set.
//
// It does not set the variable itself: withConfigDir already isolates it —
// State.Reload publishes, so every test touching a State needs that — and a
// second t.Setenv here would silently override it, pointing the test at a
// directory the code under test never writes to.
func runtimeDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		t.Fatal("XDG_RUNTIME_DIR is not isolated; call withConfigDir first")
	}
	return dir
}

// The daemon's effective routing is not derivable from config alone: in auto
// it depends on a probe verdict only the daemon holds. Everything outside
// the process — proxy status, the GUI switch — has to read it from
// somewhere, and that somewhere is this file.
func TestPublishAndReadRuntimeState(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if err := st.PublishRuntimeState(); err != nil {
		t.Fatalf("PublishRuntimeState: %v", err)
	}

	got, err := ReadRuntimeState()
	if err != nil {
		t.Fatalf("ReadRuntimeState: %v", err)
	}
	if got.Mode != proxy.ModeAuto {
		t.Errorf("Mode = %q, want %q", got.Mode, proxy.ModeAuto)
	}
	if !got.UpstreamReachable {
		t.Error("UpstreamReachable = false, want the optimistic start")
	}
	if !got.Forwarding {
		t.Error("Forwarding = false, want true while auto and reachable")
	}
	if got.UpstreamAddr != "proxy.corp:8080" {
		t.Errorf("UpstreamAddr = %q, want %q", got.UpstreamAddr, "proxy.corp:8080")
	}
}

// Forwarding is the answer the UI actually needs: in auto it is the mode AND
// the verdict together, which is exactly the combination a caller reading
// config.json alone would get wrong.
func TestRuntimeStateForwardingReflectsDegradedAuto(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	st.setReachable(false)
	if err := st.PublishRuntimeState(); err != nil {
		t.Fatalf("PublishRuntimeState: %v", err)
	}

	got, err := ReadRuntimeState()
	if err != nil {
		t.Fatalf("ReadRuntimeState: %v", err)
	}
	if got.Forwarding {
		t.Error("Forwarding = true while auto is degraded")
	}
}

// A strict pin keeps forwarding whatever the probe says.
func TestRuntimeStateUpstreamPinStaysForwarding(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"upstream","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	st.setReachable(false)
	if err := st.PublishRuntimeState(); err != nil {
		t.Fatalf("PublishRuntimeState: %v", err)
	}

	got, _ := ReadRuntimeState()
	if !got.Forwarding {
		t.Error("Forwarding = false in mode upstream; the pin must ignore the probe")
	}
}

// No file means no daemon. Readers must get that as an ordinary answer, not
// an error to special-case at every call site.
func TestReadRuntimeStateWithNoDaemon(t *testing.T) {
	withConfigDir(t)

	got, err := ReadRuntimeState()
	if err != nil {
		t.Fatalf("ReadRuntimeState with no file: %v", err)
	}
	if got.Running {
		t.Error("Running = true with no state file")
	}
}

func TestPublishRuntimeStateOverwrites(t *testing.T) {
	path := withConfigDir(t)
	dir := runtimeDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, _ := NewState(NewLogger(io.Discard, false))
	if err := st.PublishRuntimeState(); err != nil {
		t.Fatal(err)
	}
	st.setReachable(false)
	if err := st.PublishRuntimeState(); err != nil {
		t.Fatal(err)
	}

	got, _ := ReadRuntimeState()
	if got.UpstreamReachable {
		t.Error("the second publish did not replace the first")
	}
	// No temp files left behind by the atomic write.
	entries, _ := os.ReadDir(filepath.Join(dir, "proxy-helper"))
	if len(entries) != 1 {
		t.Errorf("runtime dir holds %d entries, want exactly the state file", len(entries))
	}
}
