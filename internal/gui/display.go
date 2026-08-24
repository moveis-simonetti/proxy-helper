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
func displayAvailable(lookupEnv func(string) (string, bool)) bool {
	if _, ok := lookupEnv("DISPLAY"); ok {
		return true
	}
	if _, ok := lookupEnv("WAYLAND_DISPLAY"); ok {
		return true
	}
	return false
}

// DisplayAvailable reports whether a graphical session appears to be
// present in the current process environment.
func DisplayAvailable() bool {
	return displayAvailable(os.LookupEnv)
}
