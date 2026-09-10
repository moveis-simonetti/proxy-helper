package serve

import (
	"io"
	"testing"

	"proxy-helper/internal/proxy"
)

// A profile stays defined and selected while the mode says direct — that is
// the whole point of the cheap toggle. The daemon must honour the mode, not
// the presence of a profile.
func TestModeDirectRoutesDirectWithProfileSelected(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"direct","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if up := st.Router().Route("example.com"); up.Kind != KindDirect {
		t.Errorf("Route = %v, want DIRECT while mode is direct", up)
	}
}

func TestModeUpstreamForwards(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"upstream","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	up := st.Router().Route("example.com")
	if up.Kind != KindHTTP || up.Addr != "proxy.corp:8080" {
		t.Errorf("Route = %v, want proxy.corp:8080 over HTTP", up)
	}
}

// auto forwards while the upstream answers and degrades to direct when it
// stops, without a reload.
func TestModeAutoFollowsReachability(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	// Optimistic start: with no probe result yet, never degrade.
	if up := st.Router().Route("example.com"); up.Kind != KindHTTP {
		t.Errorf("Route = %v, want the upstream before any probe has failed", up)
	}

	st.setReachable(false)
	if up := st.Router().Route("example.com"); up.Kind != KindDirect {
		t.Errorf("Route = %v, want DIRECT once the upstream is unreachable", up)
	}

	st.setReachable(true)
	if up := st.Router().Route("example.com"); up.Kind != KindHTTP {
		t.Errorf("Route = %v, want the upstream once it answers again", up)
	}
}

// upstream is the strict pin: it must keep forwarding even when the probe
// says the upstream is down, so traffic never silently leaves the proxy.
func TestModeUpstreamIgnoresReachability(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"upstream","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	st.setReachable(false)
	if up := st.Router().Route("example.com"); up.Kind != KindHTTP {
		t.Errorf("Route = %v, want the upstream: mode upstream is a strict pin", up)
	}
}

// Reload must be able to change only the mode, with the profile untouched.
func TestReloadPicksUpModeChange(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"upstream","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}

	writeConfig(t, path, `{"mode":"direct","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	if err := st.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if up := st.Router().Route("example.com"); up.Kind != KindDirect {
		t.Errorf("Route = %v, want DIRECT after reloading into direct", up)
	}
}

// Changing the upstream address must clear a stale "down" verdict, or the
// new upstream inherits the old one's failure and stays stuck on direct.
func TestReloadToNewUpstreamClearsStaleDownVerdict(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"a","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"},
		"b":{"scheme":"http","host":"b.corp","port":"3128"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	st.setReachable(false)

	writeConfig(t, path, `{"mode":"auto","active_profile":"b","profiles":{
		"a":{"scheme":"http","host":"a.corp","port":"8080"},
		"b":{"scheme":"http","host":"b.corp","port":"3128"}}}`)
	if err := st.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	up := st.Router().Route("example.com")
	if up.Kind != KindHTTP || up.Addr != "b.corp:3128" {
		t.Errorf("Route = %v, want b.corp:3128: a new upstream starts optimistic", up)
	}
}

// A legacy config (no mode) must keep behaving exactly as it did.
func TestLegacyConfigWithoutModeStillForwards(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if up := st.Router().Route("example.com"); up.Kind != KindHTTP {
		t.Errorf("Route = %v, want the upstream for a migrated legacy config", up)
	}
	if got := st.Mode(); got != proxy.ModeUpstream {
		t.Errorf("Mode = %q, want %q", got, proxy.ModeUpstream)
	}
}

// UpstreamAddr is what the prober dials; direct has nothing to probe.
func TestUpstreamAddrIsEmptyInDirect(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"direct","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)

	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	if got := st.UpstreamAddr(); got != "" {
		t.Errorf("UpstreamAddr = %q, want empty in direct mode", got)
	}
}
