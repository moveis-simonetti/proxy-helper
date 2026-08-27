package gui

import "testing"

func TestTrayIconReflectsTheProxyState(t *testing.T) {
	if got := trayIconFor(true, true); got != trayIconOn {
		t.Errorf("ligado = %q, want %q", got, trayIconOn)
	}
	if got := trayIconFor(true, false); got != trayIconOff {
		t.Errorf("desligado = %q, want %q", got, trayIconOff)
	}
}

// An icon name missing from the theme fails silently — no icon, no error. A
// local build would otherwise put an invisible item in the panel.
func TestTrayIconFallsBackWhenOurIconsAreNotInstalled(t *testing.T) {
	for _, on := range []bool{true, false} {
		if got := trayIconFor(false, on); got != trayIconFallback {
			t.Errorf("sem ícones instalados (on=%v) = %q, want %q", on, got, trayIconFallback)
		}
	}
}
