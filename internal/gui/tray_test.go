//go:build gui

package gui

import "testing"

// TestNilTraySafe is the regression test for tray's central invariant: a
// desktop with no tray must never force any of tray's methods to be guarded
// by a caller-side nil check. It runs with no display — pure defensive
// logic on a nil receiver, nothing GTK needs to be initialized for.
func TestNilTraySafe(t *testing.T) {
	var tr *tray

	if got := tr.hasIndicator(); got != false {
		t.Errorf("nil tray hasIndicator() = %v, want false", got)
	}
	if got := tr.closeToTray(); got != false {
		t.Errorf("nil tray closeToTray() = %v, want false", got)
	}
}
