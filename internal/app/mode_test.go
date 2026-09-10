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
