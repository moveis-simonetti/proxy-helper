package app

import (
	"errors"
	"testing"

	"proxy-helper/internal/proxy"
)

func TestOnActivatesTheLastProfile(t *testing.T) {
	isolateConfig(t, `{"last_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	got, err := On(d, &proxy.Executor{}, "")
	if err != nil {
		t.Fatalf("On: %v", err)
	}
	if got.Profile != "corp" {
		t.Errorf("Profile = %q, want %q", got.Profile, "corp")
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "corp")
	}
}

func TestOnWithAnExplicitNameOverridesTheLastProfile(t *testing.T) {
	isolateConfig(t, `{"last_profile":"corp","profiles":{"corp":{"host":"p.example"},"casa":{"host":"h.example"}}}`)

	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return nil }}
	got, err := On(d, &proxy.Executor{}, "casa")
	if err != nil {
		t.Fatalf("On: %v", err)
	}
	if got.Profile != "casa" {
		t.Errorf("Profile = %q, want %q", got.Profile, "casa")
	}
}

// A reload that fails must surface: the daemon is then serving a profile
// the config no longer names, and silently swallowing that is how a user
// ends up debugging traffic going somewhere they did not choose.
func TestOnSurfacesAFailedReload(t *testing.T) {
	isolateConfig(t, `{"last_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return errors.New("boom") }}
	if _, err := On(d, &proxy.Executor{}, ""); err == nil {
		t.Fatal("On succeeded with a failing ReloadDaemon; want an error")
	}
}

func TestOffRemembersWhatItTurnedOff(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	got, err := Off(d, &proxy.Executor{})
	if err != nil {
		t.Fatalf("Off: %v", err)
	}
	if got.AlreadyOff {
		t.Error("AlreadyOff = true, want false")
	}
	if got.Previous != "corp" {
		t.Errorf("Previous = %q, want %q", got.Previous, "corp")
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "" {
		t.Errorf("active_profile = %q, want empty", pf.ActiveProfile)
	}
	// Off must remember the profile, or "proxy on" afterwards has nothing
	// to restore.
	if pf.LastProfile != "corp" {
		t.Errorf("last_profile = %q, want %q", pf.LastProfile, "corp")
	}
}

// Turning off what is already off is a no-op, not an error — and it must
// not reload the daemon, which would be a pointless SIGHUP.
func TestOffOnAnAlreadyOffConfigDoesNothing(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	got, err := Off(d, &proxy.Executor{})
	if err != nil {
		t.Fatalf("Off: %v", err)
	}
	if !got.AlreadyOff {
		t.Error("AlreadyOff = false, want true")
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}
}

// Route 2: with the plumbing already in place, enabling a profile is pure
// state. Touching a target here would tear the plumbing down and write the
// upstream credential into every tool's config file — the exact thing
// --via-local exists to prevent.
func TestEnableLeavesTargetsAloneWhenAlreadyPlumbed(t *testing.T) {
	isolateConfig(t, `{"via_local":true,"profiles":{"corp":{"host":"p.example","user":"u","pass":"s3cr3t"}}}`)

	tg := &fakeTarget{name: "git", available: true}
	d := depsFor(tg)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	res, err := Enable(d, &proxy.Executor{}, "corp", []string{"all"}, false)
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !res.TargetsUntouched {
		t.Error("TargetsUntouched = false, want true")
	}
	if len(tg.setCfgs) != 0 {
		t.Errorf("target Set called %d times, want 0", len(tg.setCfgs))
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "corp")
	}
}

// Route 3: the activation is recorded before any target is touched. A target
// failing for a mundane reason used to leave the machine with everything
// configured and no active profile — the partial state this tool exists to
// prevent.
func TestEnableRecordsTheProfileEvenWhenATargetFails(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	// activeAtSet is read from disk from *inside* Set — not after Enable
	// returns — so the assertion actually exercises the ordering it claims
	// to protect. Apply never surfaces a per-target failure as an error (it
	// only records it in the Report), so a check made after Enable returns
	// would pass regardless of which happened first.
	var activeAtSet string
	tg := &fakeTarget{name: "git", available: true, setErr: errors.New("boom")}
	tg.onSet = func() {
		pf, _ := proxy.LoadProfiles()
		activeAtSet = pf.ActiveProfile
	}
	d := depsFor(tg)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	res, err := Enable(d, &proxy.Executor{}, "corp", []string{"all"}, false)
	if err != nil {
		t.Fatalf("Enable returned a global error for a per-target failure: %v", err)
	}
	if res.Report == nil {
		t.Fatal("Report is nil on the applying route")
	}
	if res.Report.Err() == nil {
		t.Error("Report.Err() is nil though a target failed")
	}

	if activeAtSet != "corp" {
		t.Errorf("active_profile at Set time = %q, want %q — must be recorded before touching targets", activeAtSet, "corp")
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "corp")
	}
}

func TestEnableRejectsAnUnknownProfile(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	d := depsFor()
	if _, err := Enable(d, &proxy.Executor{}, "naoexiste", []string{"all"}, false); err == nil {
		t.Fatal("Enable succeeded for a profile that does not exist")
	}
}

// Route 1: active_profile gets the NAME, never the reserved "_current" copy,
// which would go stale the moment the profile is edited.
func TestEnableViaLocalRecordsTheNameNotTheCurrentCopy(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	tg := &fakeTarget{name: "git", available: true}
	d := depsFor(tg)
	d.DaemonActive = func() bool { return true }
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	if _, err := Enable(d, &proxy.Executor{}, "corp", []string{"all"}, true); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "corp" {
		t.Errorf("active_profile = %q, want %q", pf.ActiveProfile, "corp")
	}
}

func TestDisableRefusesANameThatIsNotTheActiveOne(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"p.example"},"casa":{"host":"h.example"}}}`)

	d := depsFor()
	if _, err := Disable(d, &proxy.Executor{}, "casa", []string{"all"}); err == nil {
		t.Fatal("Disable accepted a profile that is not the active one")
	}
}

func TestDisableRefusesWhenNothingIsActive(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	d := depsFor()
	if _, err := Disable(d, &proxy.Executor{}, "", []string{"all"}); err == nil {
		t.Fatal("Disable succeeded with no active profile")
	}
}

// Disable clears the targets first and only then drops the state: Clear takes
// the profile lock itself, and flock does not nest within a process.
func TestDisableClearsTargetsAndThenTheState(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	tg := &fakeTarget{name: "git", available: true}
	d := depsFor(tg)
	d.ReloadDaemon = func(*proxy.Executor) error { return nil }

	rep, err := Disable(d, &proxy.Executor{}, "corp", []string{"all"})
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if rep == nil {
		t.Fatal("Report is nil")
	}
	if tg.unsets != 1 {
		t.Errorf("target Unset called %d times, want 1", tg.unsets)
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "" {
		t.Errorf("active_profile = %q, want empty", pf.ActiveProfile)
	}
	if pf.LastProfile != "corp" {
		t.Errorf("last_profile = %q, want %q", pf.LastProfile, "corp")
	}
}
