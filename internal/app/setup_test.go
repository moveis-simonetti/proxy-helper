package app

import (
	"testing"

	"proxy-helper/internal/proxy"
)

// setupDeps wires the fakes Setup needs: the target set, a daemon that can
// be installed and reports itself active afterwards, and a reload counter.
func setupDeps(t *testing.T, active *bool, installs *int, targets ...*fakeTarget) Deps {
	t.Helper()
	d := depsFor(targets...)
	d.DaemonActive = func() bool { return *active }
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }
	// Mirrors the real InstallDaemon, which writes through the Executor and
	// therefore only previews under --dry-run. A fake that installed
	// regardless would make the dry-run test assert the wrong thing.
	d.InstallDaemon = func(ex *proxy.Executor) error {
		if ex.DryRun {
			return nil
		}
		*installs++
		*active = true
		return nil
	}
	return d
}

// The point of setup: one command takes a machine from nothing to targets
// pointing at the daemon, with the mode ready for the cheap toggle.
func TestSetupInstallsDaemonAppliesTargetsAndSetsMode(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	active, installs := false, 0
	tg := &fakeTarget{name: "git", available: true}
	d := setupDeps(t, &active, &installs, tg)

	res, err := Setup(d, &proxy.Executor{}, SetupOptions{
		Profile: "corp",
		Config:  proxy.Config{Scheme: "http", Host: "proxy.corp", Port: "3128"},
		Targets: []string{"all"},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if installs != 1 || !res.DaemonInstalled {
		t.Errorf("installs = %d, DaemonInstalled = %v; want the daemon installed once", installs, res.DaemonInstalled)
	}
	if len(tg.setCfgs) != 1 {
		t.Fatalf("target Set called %d times, want 1", len(tg.setCfgs))
	}
	// The target gets the loopback daemon, never the upstream credentials —
	// that separation is the whole reason via-local exists.
	got := tg.setCfgs[0]
	if got.Host != "127.0.0.1" {
		t.Errorf("target host = %q, want the local daemon", got.Host)
	}
	if got.Username != "" || got.Password != "" {
		t.Errorf("credentials leaked into the target: %+v", got)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.EffectiveMode() != proxy.ModeAuto {
		t.Errorf("mode = %q, want %q", pf.EffectiveMode(), proxy.ModeAuto)
	}
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "corp")
	}
	if !pf.ViaLocal {
		t.Error("via_local = false; the cheap toggle only works with it on")
	}
	if _, ok := pf.Get("corp"); !ok {
		t.Error("the profile was not saved")
	}
}

// A daemon that is already running must not be reinstalled: setup is safe to
// re-run, and reinstalling would restart the proxy under live connections.
func TestSetupLeavesARunningDaemonAlone(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	active, installs := true, 0
	tg := &fakeTarget{name: "git", available: true}
	d := setupDeps(t, &active, &installs, tg)

	res, err := Setup(d, &proxy.Executor{}, SetupOptions{
		Profile: "corp",
		Config:  proxy.Config{Scheme: "http", Host: "proxy.corp", Port: "3128"},
		Targets: []string{"all"},
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if installs != 0 {
		t.Errorf("installs = %d, want 0 for an already-running daemon", installs)
	}
	if res.DaemonInstalled {
		t.Error("DaemonInstalled = true, want false")
	}
}

// Re-running setup against a saved profile, with no new connection details,
// must reuse what is stored rather than demand it again.
func TestSetupReusesAnExistingProfile(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"scheme":"http","host":"saved.corp","port":"9999"}}}`)

	active, installs := true, 0
	tg := &fakeTarget{name: "git", available: true}
	d := setupDeps(t, &active, &installs, tg)

	if _, err := Setup(d, &proxy.Executor{}, SetupOptions{
		Profile: "corp",
		Targets: []string{"all"},
	}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	pf, _ := proxy.LoadProfiles()
	cfg, _ := pf.Get("corp")
	if cfg.Host != "saved.corp" || cfg.Port != "9999" {
		t.Errorf("stored profile was overwritten: %+v", cfg)
	}
}

func TestSetupWithNoProfileAndNoConfigFails(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	active, installs := true, 0
	d := setupDeps(t, &active, &installs, &fakeTarget{name: "git", available: true})

	if _, err := Setup(d, &proxy.Executor{}, SetupOptions{Profile: "ghost", Targets: []string{"all"}}); err == nil {
		t.Fatal("Setup with an unknown profile and no config = nil, want an error")
	}
}

// The mode is an option so "setup --mode upstream" can pin a machine that
// must never bypass the proxy.
func TestSetupHonoursAnExplicitMode(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	active, installs := true, 0
	d := setupDeps(t, &active, &installs, &fakeTarget{name: "git", available: true})

	if _, err := Setup(d, &proxy.Executor{}, SetupOptions{
		Profile: "corp",
		Config:  proxy.Config{Scheme: "http", Host: "proxy.corp", Port: "3128"},
		Targets: []string{"all"},
		Mode:    proxy.ModeUpstream,
	}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.EffectiveMode() != proxy.ModeUpstream {
		t.Errorf("mode = %q, want %q", pf.EffectiveMode(), proxy.ModeUpstream)
	}
}

// Dry-run must describe the whole plan without installing a unit or writing
// a profile — otherwise "let me see what this would do" changes the machine.
func TestSetupDryRunTouchesNothing(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	active, installs := false, 0
	tg := &fakeTarget{name: "git", available: true}
	d := setupDeps(t, &active, &installs, tg)

	if _, err := Setup(d, &proxy.Executor{DryRun: true}, SetupOptions{
		Profile: "corp",
		Config:  proxy.Config{Scheme: "http", Host: "proxy.corp", Port: "3128"},
		Targets: []string{"all"},
	}); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if installs != 0 {
		t.Errorf("installs = %d in dry-run, want 0", installs)
	}
	pf, _ := proxy.LoadProfiles()
	if _, ok := pf.Get("corp"); ok {
		t.Error("dry-run saved the profile")
	}
	if pf.ViaLocal {
		t.Error("dry-run set via_local")
	}
}

// With the targets pointing at the daemon, disabling a profile has no reason
// to rewrite thirteen configurations and ask for a password: the routing
// decision lives in the daemon, so dropping to direct is the whole job.
func TestDisableWithViaLocalDoesNotTouchTargets(t *testing.T) {
	isolateConfig(t, `{"mode":"auto","active_profile":"corp","via_local":true,
		"profiles":{"corp":{"host":"p.example"}}}`)

	tg := &fakeTarget{name: "git", available: true}
	d := depsFor(tg)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := Disable(d, &proxy.Executor{}, "corp", []string{"all"}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if tg.unsets != 0 {
		t.Errorf("target Unset called %d times, want 0 with via_local on", tg.unsets)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.EffectiveMode() != proxy.ModeDirect {
		t.Errorf("mode = %q, want %q", pf.EffectiveMode(), proxy.ModeDirect)
	}
	// The plumbing stays: only "proxy unset" takes the targets off the
	// daemon, and that is the documented recovery path, not a daily action.
	if !pf.ViaLocal {
		t.Error("via_local was cleared; disable must not undo the plumbing")
	}
}
