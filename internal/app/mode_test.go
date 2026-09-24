package app

import (
	"testing"

	"proxy-helper/internal/proxy"
)

func TestSetModeWritesStateAndReloads(t *testing.T) {
	isolateConfig(t, `{"mode":"upstream","active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	res, err := SetMode(d, &proxy.Executor{}, proxy.ModeDirect)
	if err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if res.Previous != proxy.ModeUpstream || res.Mode != proxy.ModeDirect {
		t.Errorf("result = %+v, want upstream -> direct", res)
	}
	// The whole point of the mode: a SIGHUP, and not one target rewritten.
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.EffectiveMode() != proxy.ModeDirect {
		t.Errorf("persisted mode = %q, want %q", pf.EffectiveMode(), proxy.ModeDirect)
	}
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want it still selected", pf.ActiveProfile)
	}
}

// Setting the mode it already has must not SIGHUP the daemon for nothing.
func TestSetModeToTheSameModeIsANoOp(t *testing.T) {
	isolateConfig(t, `{"mode":"direct","active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	res, err := SetMode(d, &proxy.Executor{}, proxy.ModeDirect)
	if err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if !res.Unchanged {
		t.Error("Unchanged = false, want true")
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0 for a no-op", reloads)
	}
}

// A forwarding mode with nothing selected is a state the daemon cannot act
// on, so it is refused before anything is written.
func TestSetModeForwardingWithoutProfileFails(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	if _, err := SetMode(d, &proxy.Executor{}, proxy.ModeAuto); err == nil {
		t.Fatal("SetMode(auto) with no profile = nil, want an error")
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0 after a refused change", reloads)
	}
}

func TestCurrentModeReadsConfig(t *testing.T) {
	isolateConfig(t, `{"mode":"auto","active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	got, profile, err := CurrentMode()
	if err != nil {
		t.Fatalf("CurrentMode: %v", err)
	}
	if got != proxy.ModeAuto {
		t.Errorf("mode = %q, want %q", got, proxy.ModeAuto)
	}
	if profile != "corp" {
		t.Errorf("profile = %q, want %q", profile, "corp")
	}
}

// Going direct means nothing is proxied any more, so nm-connectivity's
// check — valid only while traffic actually flows through the proxy — must
// be cleared, not left pointing at a network the daemon is no longer using.
func TestSetModeToDirectClearsConnectivityCheck(t *testing.T) {
	isolateConfig(t, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"host":"p.example","connectivity_check_url":"http://p.example/","connectivity_check_response":"<html>"}}}`)

	nm := &fakeTarget{name: "nm-connectivity", available: true}
	d := depsFor(nm)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := SetMode(d, &proxy.Executor{}, proxy.ModeDirect); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if len(nm.setCfgs) != 1 {
		t.Fatalf("nm-connectivity Set called %d times, want 1", len(nm.setCfgs))
	}
	if got := nm.setCfgs[0].ConnectivityCheckURL; got != "" {
		t.Errorf("Set received ConnectivityCheckURL %q, want empty — direct means nothing is proxied", got)
	}
}

// The reverse of the test above: leaving direct means traffic is proxied
// again, so the check has to come back for whoever is now active.
func TestSetModeFromDirectReappliesConnectivityCheck(t *testing.T) {
	isolateConfig(t, `{"mode":"direct","active_profile":"corp","profiles":{
		"corp":{"host":"p.example","connectivity_check_url":"http://p.example/","connectivity_check_response":"<html>"}}}`)

	nm := &fakeTarget{name: "nm-connectivity", available: true}
	d := depsFor(nm)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := SetMode(d, &proxy.Executor{}, proxy.ModeAuto); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if len(nm.setCfgs) != 1 {
		t.Fatalf("nm-connectivity Set called %d times, want 1", len(nm.setCfgs))
	}
	if got := nm.setCfgs[0].ConnectivityCheckURL; got != "http://p.example/" {
		t.Errorf("Set received ConnectivityCheckURL %q, want the profile's own", got)
	}
}

// The overwhelmingly common case — nobody configured this — must stay
// exactly what the mode docstring promises: a SIGHUP, no target rewritten.
func TestSetModeWithoutConnectivityCheckTouchesNoTarget(t *testing.T) {
	isolateConfig(t, `{"mode":"auto","active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	nm := &fakeTarget{name: "nm-connectivity", available: true}
	d := depsFor(nm)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := SetMode(d, &proxy.Executor{}, proxy.ModeDirect); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if len(nm.setCfgs) != 0 {
		t.Errorf("nm-connectivity touched %d time(s), want 0 for a profile that never configured it", len(nm.setCfgs))
	}
}

// Off shares SetMode's transition into direct, so it must clear the check
// too — it is the command someone reaches for exactly when leaving a
// proxy-only network (e.g. switching to a phone hotspot).
func TestOffClearsConnectivityCheck(t *testing.T) {
	isolateConfig(t, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"host":"p.example","connectivity_check_url":"http://p.example/","connectivity_check_response":"<html>"}}}`)

	nm := &fakeTarget{name: "nm-connectivity", available: true}
	d := depsFor(nm)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := Off(d, &proxy.Executor{}); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if len(nm.setCfgs) != 1 || nm.setCfgs[0].ConnectivityCheckURL != "" {
		t.Errorf("Off must clear nm-connectivity, got setCfgs=%+v", nm.setCfgs)
	}
}

// On shares SetMode's transition out of direct, so it must reapply the
// check for whichever profile ends up active.
func TestOnReappliesConnectivityCheck(t *testing.T) {
	isolateConfig(t, `{"mode":"direct","active_profile":"corp","profiles":{
		"corp":{"host":"p.example","connectivity_check_url":"http://p.example/","connectivity_check_response":"<html>"}}}`)

	nm := &fakeTarget{name: "nm-connectivity", available: true}
	d := depsFor(nm)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := On(d, &proxy.Executor{}, ""); err != nil {
		t.Fatalf("On: %v", err)
	}
	if len(nm.setCfgs) != 1 || nm.setCfgs[0].ConnectivityCheckURL != "http://p.example/" {
		t.Errorf("On must reapply nm-connectivity, got setCfgs=%+v", nm.setCfgs)
	}
}

// on/off are the friendly names for two of the three modes and must stay in
// step with SetMode rather than growing their own state handling.
func TestOnOffAgreeWithSetMode(t *testing.T) {
	isolateConfig(t, `{"mode":"direct","active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return nil }}

	if _, err := On(d, &proxy.Executor{}, ""); err != nil {
		t.Fatalf("On: %v", err)
	}
	pf, _ := proxy.LoadProfiles()
	if !pf.EffectiveMode().Forwards() {
		t.Errorf("mode = %q after On, want a forwarding mode", pf.EffectiveMode())
	}

	if _, err := Off(d, &proxy.Executor{}); err != nil {
		t.Fatalf("Off: %v", err)
	}
	pf, _ = proxy.LoadProfiles()
	if pf.EffectiveMode() != proxy.ModeDirect {
		t.Errorf("mode = %q after Off, want %q", pf.EffectiveMode(), proxy.ModeDirect)
	}
}
