package app

import (
	"testing"

	"proxy-helper/internal/proxy"
)

// stubProbe returns a fixed answer, so the tests never depend on what is
// listening on the developer's machine.
func stubProbe(addrs ...string) DaemonChecks {
	return DaemonChecks{Listening: func(int) []string { return addrs }}
}

// The failure this exists to catch: every target points at 127.0.0.1:8888,
// nothing is listening there, and the machine has no network at all — with
// nothing on screen connecting the two.
func TestStrandedWhenViaLocalAndNothingListening(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, stubProbe())
	if !h.Stranded() {
		t.Error("Stranded = false with via_local on and no listener")
	}
	if h.Port != 8888 {
		t.Errorf("Port = %d, want 8888", h.Port)
	}
}

func TestNotStrandedWhenTheDaemonAnswers(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, stubProbe("127.0.0.1:8888"))
	if h.Stranded() {
		t.Error("Stranded = true while the daemon answers")
	}
}

// Without via_local the targets carry the upstream themselves, so a stopped
// daemon costs nothing and warning about it would be noise.
func TestNotStrandedWithoutViaLocal(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: false, LocalPort: 8888}

	h := CheckDaemon(pf, stubProbe())
	if h.Stranded() {
		t.Error("Stranded = true without via_local; a stopped daemon is harmless then")
	}
}

// systemd reporting the unit as active is not proof that anything is bound:
// a daemon that failed to bind, or is mid-restart, is active and useless.
// The probe is the authority, which is why CheckDaemon takes one.
func TestCheckDaemonUsesTheProbeNotTheUnitState(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 9090}

	var asked int
	h := CheckDaemon(pf, DaemonChecks{Listening: func(port int) []string {
		asked = port
		return nil
	}})
	if asked != 9090 {
		t.Errorf("probed port %d, want the configured 9090", asked)
	}
	if !h.Stranded() {
		t.Error("Stranded = false; the probe found nothing bound")
	}
}

func TestCheckDaemonDefaultsThePort(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true}

	h := CheckDaemon(pf, stubProbe())
	if h.Port != proxy.DefaultLocalPort {
		t.Errorf("Port = %d, want the default %d", h.Port, proxy.DefaultLocalPort)
	}
}

// checks builds a DaemonChecks with fixed answers.
func checks(listening []string, publishedPID int, published bool, mainPID int) DaemonChecks {
	return DaemonChecks{
		Listening: func(int) []string { return listening },
		Published: func() (int, bool) { return publishedPID, published },
		MainPID:   func() int { return mainPID },
	}
}

// The upgrade trap: replacing the binary on disk does not restart the
// daemon, so the process keeps running the previous build. A daemon from
// before the mode field reads active_profile and forwards regardless of what
// the mode says — every toggle writes config, sends a SIGHUP, and nothing
// happens. Silently accepting that is the worst outcome, so it has to be
// detectable.
func TestOutdatedWhenTheRunningDaemonPublishesNothing(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, checks([]string{"127.0.0.1:8888"}, 0, false, 3916))
	if !h.Outdated() {
		t.Error("Outdated = false for a listening daemon that publishes no state")
	}
	if h.Stranded() {
		t.Error("Stranded = true; something IS listening")
	}
}

// A state file left behind by a killed daemon names a PID that is no longer
// the unit's. Trusting it would report a healthy daemon that does not exist.
func TestOutdatedWhenTheStateFileIsStale(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, checks([]string{"127.0.0.1:8888"}, 1234, true, 3916))
	if !h.Outdated() {
		t.Error("Outdated = false with the state file naming a different PID")
	}
}

func TestNotOutdatedWhenPidsAgree(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, checks([]string{"127.0.0.1:8888"}, 3916, true, 3916))
	if h.Outdated() {
		t.Error("Outdated = true while the publishing daemon is the running one")
	}
}

// A daemon started by hand in a terminal has no systemd MainPID. Calling
// that outdated would nag every developer running it in the foreground.
func TestNotOutdatedWithoutSystemd(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, checks([]string{"127.0.0.1:8888"}, 0, false, 0))
	if h.Outdated() {
		t.Error("Outdated = true for a daemon systemd does not manage")
	}
}

// Nothing listening is the stranded case, which is a different and louder
// problem; reporting both at once would just be noise.
func TestStrandedIsNotAlsoOutdated(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: true, LocalPort: 8888}

	h := CheckDaemon(pf, checks(nil, 0, false, 3916))
	if !h.Stranded() {
		t.Error("Stranded = false with nothing listening")
	}
	if h.Outdated() {
		t.Error("Outdated = true on top of stranded; one problem at a time")
	}
}

func TestNotOutdatedWithoutViaLocal(t *testing.T) {
	pf := &proxy.ProfileFile{ViaLocal: false, LocalPort: 8888}

	h := CheckDaemon(pf, checks([]string{"127.0.0.1:8888"}, 0, false, 3916))
	if h.Outdated() {
		t.Error("Outdated = true without via_local; the daemon is not in the path")
	}
}
