//go:build gui

// Package gui implements the GTK3 graphical interface for proxy-helper.
// Everything in this package requires cgo and GTK3 development headers, and
// is only reachable when the binary is built with the "gui" build tag.
package gui

import (
	"fmt"

	"github.com/gotk3/gotk3/gtk"
)

// Run initializes GTK, shows the main window, and blocks until the window is
// closed. It must be called from the goroutine that will host the GTK main
// loop for the lifetime of the process: GTK is not thread-safe, and its main
// loop must stay pinned to a single OS thread.
//
// runtime.LockOSThread is deliberately NOT called here: it must happen
// before gtk.Init and before any other GTK call, but also provably on the
// process's main OS thread, which a call inside Run can only guarantee by
// assuming its caller never hops goroutines before reaching here. That lock
// is taken once, unconditionally, at the top of main() (see main.go) —
// before cmd.Execute() — which is provably the main thread. Do not add a
// second LockOSThread call here; the pairing with UnlockOSThread would be
// wrong for a lock meant to hold for the process's entire lifetime anyway.
func Run() error {
	// GTK blocks indefinitely trying to open a display connection when
	// neither X11 nor Wayland is available (e.g. a plain SSH session) —
	// it neither errors out nor prints anything, it just hangs. Fail fast
	// with a clear message instead of leaving the user with a frozen
	// terminal they have to Ctrl+C.
	if !DisplayAvailable() {
		return fmt.Errorf("no graphical session found (DISPLAY and WAYLAND_DISPLAY are both unset); the \"gui\" command requires a desktop session, use the other proxy-helper commands instead")
	}

	gtk.Init(nil)

	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return err
	}

	win.SetTitle("proxy-helper")
	win.SetDefaultSize(1000, 720)
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	win.ShowAll()
	gtk.Main()

	return nil
}
