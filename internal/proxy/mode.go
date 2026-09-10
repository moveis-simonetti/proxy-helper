package proxy

import "fmt"

// Mode is what the daemon does with a request: forward it to the upstream
// proxy, send it straight out, or decide per reachability.
//
// It exists because active_profile used to answer two different questions —
// "which upstream" and "use it at all" — and could not answer the second
// without destroying the first. That forced the last_profile stash: turning
// the proxy off cleared the selection, so turning it back on had to guess
// what the user had been using. With the routing decision in its own field,
// the profile stays selected while traffic goes direct, and auto becomes
// expressible at all: probing needs an address, which a cleared
// active_profile does not have.
type Mode string

const (
	// ModeAuto forwards while the upstream answers and falls back to direct
	// when it stops. It is the mode for a laptop that moves between a
	// proxy-only network and an open one.
	ModeAuto Mode = "auto"
	// ModeUpstream always forwards. It is the strict pin for anyone who
	// must never let traffic leave without going through the proxy.
	ModeUpstream Mode = "upstream"
	// ModeDirect never forwards. Targets keep pointing at the daemon, so
	// this is the cheap "off": no target is rewritten and no sudo is asked.
	ModeDirect Mode = "direct"
)

// Forwards reports whether the mode can send traffic upstream, which is what
// makes an active profile mandatory.
func (m Mode) Forwards() bool { return m == ModeAuto || m == ModeUpstream }

// validMode reports whether s names a mode this build understands.
func validMode(s string) bool {
	switch Mode(s) {
	case ModeAuto, ModeUpstream, ModeDirect:
		return true
	}
	return false
}

// ParseMode converts user input into a Mode, listing the alternatives when
// it cannot.
func ParseMode(s string) (Mode, error) {
	if !validMode(s) {
		return "", fmt.Errorf("unknown mode %q (want auto, upstream or direct)", s)
	}
	return Mode(s), nil
}

// Normalize fills in Mode for a config written before the field existed, and
// repairs one written by something this build does not understand. It runs
// on every load and changes only memory; the value reaches disk on the next
// Save.
//
// The unknown-mode case falls back to direct rather than erroring: a config
// from a newer version, or a hand-edited typo, must not take the daemon down
// or silently route traffic somewhere the user did not ask for.
func (pf *ProfileFile) Normalize() {
	if validMode(pf.Mode) {
		return
	}
	if pf.Mode != "" {
		pf.Mode = string(ModeDirect)
		return
	}

	switch {
	case pf.ActiveProfile != "":
		// Legacy "on": an active profile meant proxying. Upstream, not
		// auto — nobody gets promoted to a probing mode they never chose.
		pf.Mode = string(ModeUpstream)
	case pf.LastProfile != "":
		// Legacy "off". The stashed name becomes the selection again, so
		// the profile survives the migration visible instead of hidden.
		pf.Mode = string(ModeDirect)
		pf.ActiveProfile = pf.LastProfile
	default:
		pf.Mode = string(ModeDirect)
	}
}

// EffectiveMode returns the configured mode, treating anything unset or
// unrecognised as direct. Callers use it instead of reading Mode directly so
// a config that never went through Normalize still answers safely.
func (pf *ProfileFile) EffectiveMode() Mode {
	if validMode(pf.Mode) {
		return Mode(pf.Mode)
	}
	return ModeDirect
}

// SetMode records the routing decision. Forwarding modes need somewhere to
// forward to, so they are refused without an active profile — accepting them
// would store a mode the daemon cannot act on.
func (pf *ProfileFile) SetMode(m Mode) error {
	if !validMode(string(m)) {
		return fmt.Errorf("unknown mode %q (want auto, upstream or direct)", m)
	}
	if m.Forwards() && pf.ActiveProfile == "" {
		return fmt.Errorf("mode %q needs an active profile; select one first (see \"proxy profile list\")", m)
	}
	pf.Mode = string(m)
	return nil
}

// SelectProfile picks which upstream the forwarding modes use, and turns
// forwarding on.
//
// Selecting implies auto on purpose: choosing a profile is an explicit
// request to use it, and leaving the mode on direct would make the gesture
// do nothing the user can see. Only "proxy mode direct" goes back to direct.
func (pf *ProfileFile) SelectProfile(name string) error {
	if _, ok := pf.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found (see \"proxy profile list\")", name)
	}
	pf.ActiveProfile = name
	pf.LastProfile = name
	pf.Mode = string(ModeAuto)
	return nil
}
