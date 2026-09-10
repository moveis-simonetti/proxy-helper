package app

import (
	"testing"

	"proxy-helper/internal/proxy"
)

// stubProbe returns a fixed answer, so the tests never depend on what is
// listening on the developer's machine.
func stubProbe(addrs ...string) func(int) []string {
	return func(int) []string { return addrs }
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
	h := CheckDaemon(pf, func(port int) []string {
		asked = port
		return nil
	})
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
