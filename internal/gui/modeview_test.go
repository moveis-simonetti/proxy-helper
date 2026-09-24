package gui

import (
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

// The header shows two controls for three modes: a lock ("automático") and
// the switch itself. This mapping is what keeps them from ever contradicting
// each other or the daemon.
func TestModeSwitchState(t *testing.T) {
	cases := []struct {
		name      string
		mode      proxy.Mode
		reachable bool
		wantLock  bool
		wantOn    bool
	}{
		// In auto the switch is not an input at all: it reports what the
		// daemon is really doing, which is the mode AND the probe verdict.
		{"auto while the upstream answers", proxy.ModeAuto, true, true, true},
		{"auto while the upstream is down", proxy.ModeAuto, false, true, false},
		// Pinned modes ignore the verdict, so the switch shows the pin.
		{"upstream pin", proxy.ModeUpstream, false, false, true},
		{"direct", proxy.ModeDirect, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lock, on := modeSwitchState(c.mode, c.reachable)
			if lock != c.wantLock || on != c.wantOn {
				t.Errorf("modeSwitchState(%q, %v) = (lock %v, on %v), want (%v, %v)",
					c.mode, c.reachable, lock, on, c.wantLock, c.wantOn)
			}
		})
	}
}

// A degraded auto must not read as plain "Direto": the user needs to see
// that the machine chose it, not that they did.
func TestModeLabelDistinguishesDegradedAuto(t *testing.T) {
	if got := modeLabel(proxy.ModeDirect, true); got != "Direto" {
		t.Errorf("direct label = %q, want %q", got, "Direto")
	}
	if got := modeLabel(proxy.ModeUpstream, false); got != "Ativo" {
		t.Errorf("upstream label = %q, want %q", got, "Ativo")
	}

	degraded := modeLabel(proxy.ModeAuto, false)
	if degraded == "Direto" {
		t.Error("degraded auto reads as a plain manual Direto")
	}
	if !strings.Contains(strings.ToLower(degraded), "auto") {
		t.Errorf("degraded auto label = %q, want it to name auto", degraded)
	}

	if healthy := modeLabel(proxy.ModeAuto, true); healthy == degraded {
		t.Error("auto reads the same whether the upstream is up or down")
	}
}

// Gestures are the inverse mapping. Turning the lock on means auto; turning
// it off freezes whatever was in force, so the machine keeps doing what it
// was already doing instead of jumping.
func TestModeFromGesture(t *testing.T) {
	cases := []struct {
		name string
		lock bool
		on   bool
		want proxy.Mode
	}{
		{"lock on", true, true, proxy.ModeAuto},
		{"lock on while degraded", true, false, proxy.ModeAuto},
		{"lock off, switch on", false, true, proxy.ModeUpstream},
		{"lock off, switch off", false, false, proxy.ModeDirect},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := modeFromGesture(c.lock, c.on); got != c.want {
				t.Errorf("modeFromGesture(%v, %v) = %q, want %q", c.lock, c.on, got, c.want)
			}
		})
	}
}

// Round trip: whatever the widgets show for a mode must map back to that
// same mode, or a user who touches nothing still changes something.
func TestModeGestureRoundTrip(t *testing.T) {
	for _, m := range []proxy.Mode{proxy.ModeAuto, proxy.ModeUpstream, proxy.ModeDirect} {
		for _, reachable := range []bool{true, false} {
			lock, on := modeSwitchState(m, reachable)
			if got := modeFromGesture(lock, on); got != m {
				t.Errorf("%q (reachable=%v) round-tripped to %q", m, reachable, got)
			}
		}
	}
}

func TestModeTooltipExplainsWhyTheSwitchIsLocked(t *testing.T) {
	locked := modeTooltip(proxy.ModeAuto, false, false)
	if locked == "" {
		t.Fatal("no tooltip for a locked switch")
	}
	// A read-only switch with no explanation reads as a broken one.
	if !strings.Contains(strings.ToLower(locked), "autom") {
		t.Errorf("tooltip = %q, want it to explain the automatic mode", locked)
	}

	stranded := modeTooltip(proxy.ModeAuto, true, true)
	if stranded == locked {
		t.Error("a stranded daemon gets the same tooltip as a healthy one")
	}
}
