package proxy

import "testing"

// Normalize is the migration seam: every config written before `mode`
// existed has to land on the routing behaviour it had, and a config that
// already carries a mode must survive untouched.
func TestNormalizeMigratesLegacyConfigs(t *testing.T) {
	cases := []struct {
		name       string
		in         ProfileFile
		wantMode   Mode
		wantActive string
	}{
		{
			name:       "mode already set is kept",
			in:         ProfileFile{Mode: string(ModeAuto), ActiveProfile: "corp"},
			wantMode:   ModeAuto,
			wantActive: "corp",
		},
		{
			// A typo, or a config written by a newer version, must not take
			// the daemon down. Direct is the safe reading: it never sends
			// traffic somewhere the user did not ask for.
			name:       "unknown mode falls back to direct",
			in:         ProfileFile{Mode: "sideways", ActiveProfile: "corp"},
			wantMode:   ModeDirect,
			wantActive: "corp",
		},
		{
			// Legacy "on": an active profile meant proxying. Preserve it
			// exactly — do not silently promote anyone to auto.
			name:       "legacy active profile becomes upstream",
			in:         ProfileFile{ActiveProfile: "corp"},
			wantMode:   ModeUpstream,
			wantActive: "corp",
		},
		{
			// Legacy "off" left active empty and stashed the name. With a
			// mode field the profile can stay selected, which is the whole
			// point: the GUI stops forgetting it on every off.
			name:       "legacy off restores the stashed profile as selected",
			in:         ProfileFile{LastProfile: "corp"},
			wantMode:   ModeDirect,
			wantActive: "corp",
		},
		{
			name:       "empty config is direct",
			in:         ProfileFile{},
			wantMode:   ModeDirect,
			wantActive: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pf := c.in
			pf.Normalize()
			if got := pf.EffectiveMode(); got != c.wantMode {
				t.Errorf("mode = %q, want %q", got, c.wantMode)
			}
			if pf.ActiveProfile != c.wantActive {
				t.Errorf("ActiveProfile = %q, want %q", pf.ActiveProfile, c.wantActive)
			}
		})
	}
}

// The bug the mode field exists to kill: turning the proxy off used to clear
// active_profile, so the selector had nothing to show and On had to guess
// from last_profile.
func TestOffKeepsProfileSelected(t *testing.T) {
	pf := ProfileFile{Mode: string(ModeUpstream), ActiveProfile: "corp",
		Profiles: map[string]Config{"corp": {Host: "proxy.corp"}}}

	pf.Off()

	if pf.EffectiveMode() != ModeDirect {
		t.Errorf("mode = %q, want %q", pf.EffectiveMode(), ModeDirect)
	}
	if pf.ActiveProfile != "corp" {
		t.Errorf("ActiveProfile = %q, want it kept as %q", pf.ActiveProfile, "corp")
	}
	// last_profile is still written so a daemon from before this change,
	// still running after an upgrade, keeps working.
	if pf.LastProfile != "corp" {
		t.Errorf("LastProfile = %q, want %q for backward compatibility", pf.LastProfile, "corp")
	}
}

func TestOnRestoresSelectedProfileWithoutArgument(t *testing.T) {
	pf := ProfileFile{Mode: string(ModeDirect), ActiveProfile: "corp",
		Profiles: map[string]Config{"corp": {Host: "proxy.corp"}}}

	if err := pf.On(""); err != nil {
		t.Fatalf("On: %v", err)
	}
	if pf.EffectiveMode() != ModeAuto {
		t.Errorf("mode = %q, want %q", pf.EffectiveMode(), ModeAuto)
	}
	if pf.ActiveProfile != "corp" {
		t.Errorf("ActiveProfile = %q, want %q", pf.ActiveProfile, "corp")
	}
}

// auto and upstream both need somewhere to forward to; accepting them with
// no profile would produce a mode the daemon cannot act on.
func TestSetModeRejectsForwardingWithoutProfile(t *testing.T) {
	for _, m := range []Mode{ModeAuto, ModeUpstream} {
		pf := ProfileFile{}
		if err := pf.SetMode(m); err == nil {
			t.Errorf("SetMode(%q) with no active profile = nil, want an error", m)
		}
	}

	pf := ProfileFile{}
	if err := pf.SetMode(ModeDirect); err != nil {
		t.Errorf("SetMode(direct) with no profile = %v, want nil", err)
	}
}

func TestSelectProfileImpliesForwarding(t *testing.T) {
	pf := ProfileFile{Mode: string(ModeDirect),
		Profiles: map[string]Config{"corp": {Host: "proxy.corp"}}}

	if err := pf.SelectProfile("corp"); err != nil {
		t.Fatalf("SelectProfile: %v", err)
	}
	// Picking a profile is an explicit request to use it. Leaving the mode
	// on direct would make the gesture do nothing visible.
	if pf.EffectiveMode() != ModeAuto {
		t.Errorf("mode = %q, want %q after selecting a profile", pf.EffectiveMode(), ModeAuto)
	}

	if err := pf.SelectProfile("ghost"); err == nil {
		t.Error("SelectProfile on an undefined profile = nil, want an error")
	}
}

// SetCurrent backs "proxy set --via-local". Without a mode it would leave
// the ad-hoc profile selected but switched off.
func TestSetCurrentTurnsForwardingOn(t *testing.T) {
	pf := ProfileFile{Mode: string(ModeDirect)}
	pf.SetCurrent(Config{Host: "proxy.corp", Port: "3128"})

	if pf.ActiveProfile != CurrentProfileName {
		t.Errorf("ActiveProfile = %q, want %q", pf.ActiveProfile, CurrentProfileName)
	}
	if pf.EffectiveMode() == ModeDirect {
		t.Error("mode is still direct; the ad-hoc profile would never be used")
	}
}
