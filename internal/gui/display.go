// Package gui implements the GTK3 graphical interface for proxy-helper.
//
// This file has no "gui" build tag on purpose: the display check it
// contains must not pull in GTK/cgo, so it can run (and be tested) as part
// of the normal, non-GUI build too.
package gui

import "os"

// displayAvailable reports whether a graphical session appears to be
// present, based on DISPLAY (X11) and WAYLAND_DISPLAY (Wayland). It takes a
// lookup function so callers (and tests) don't have to mutate the process
// environment via os.Setenv/os.Unsetenv.
//
// A variable that is set but empty (DISPLAY="") is treated as absent, not
// present: that is a real state, seen in containers and some systemd units,
// and treating it as a live display would send the caller on to GTK's own
// indefinite hang instead of this package's fail-fast message.
func displayAvailable(lookupEnv func(string) (string, bool)) bool {
	if v, ok := lookupEnv("DISPLAY"); ok && v != "" {
		return true
	}
	if v, ok := lookupEnv("WAYLAND_DISPLAY"); ok && v != "" {
		return true
	}
	return false
}

// DisplayAvailable reports whether a graphical session appears to be
// present in the current process environment.
func DisplayAvailable() bool {
	return displayAvailable(os.LookupEnv)
}
