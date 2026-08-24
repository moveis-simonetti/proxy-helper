//go:build gui

// Package gui implements the GTK3 graphical interface for proxy-helper.
// Everything in this package requires cgo and GTK3 development headers, and
// is only reachable when the binary is built with the "gui" build tag.
package gui

import (
	"fmt"
	"os"

	"github.com/gotk3/gotk3/glib"
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
		return fmt.Errorf("no graphical session found (DISPLAY and WAYLAND_DISPLAY are both unset); the \"gui\" command requires a desktop session — if you're connected over SSH, reconnect with `ssh -X` or `ssh -Y`, or use the other proxy-helper commands instead")
	}

	gtk.Init(nil)

	// The runner is what keeps blocking work (Apply, Clear, ...) off the
	// GTK main loop's thread while still delivering UI updates back onto
	// it. deliver wraps glib.IdleAdd in a func() with no return value:
	// IdleAdd accepts a func() bool too, and a stray true there would
	// reschedule the callback forever and pin the UI at 100% CPU.
	jobRunner := newRunner(func(f func()) { glib.IdleAdd(f) })
	jobRunner.start()
	// stop() is idempotent, so this defer is safe alongside the window's
	// own "destroy" handler calling it too. It is what stops the process
	// from exiting mid-job if gtk.Main() ever returns some other way than
	// through destroy — a Ctrl+Q accelerator or a quit menu item calling
	// gtk.MainQuit() directly, say. Without it, Go would not wait for the
	// worker and the process could die with some proxy targets configured
	// and others not.
	defer jobRunner.stop()

	win, err := newWindow(jobRunner)
	if err != nil {
		return err
	}

	// A job that panics is caught by runner.run's recover (see jobs.go) so
	// it cannot kill the worker goroutine, but with no handler registered
	// that recovered panic only reaches stderr — invisible on a released
	// build with no terminal attached, indistinguishable from the button
	// simply not working. Wiring it to a dialog here is what makes that
	// failure mode visible instead of silent.
	jobRunner.setPanicHandler(func(v any) {
		info, ok := v.(jobPanic)
		if !ok {
			// Should not happen: handlePanic always wraps the recovered
			// value in a jobPanic before calling onPanic. Fall back to
			// stderr rather than losing the report if that ever changes.
			fmt.Fprintf(os.Stderr, "gui: job panicked: %v\n", v)
			return
		}
		if err := showPanicDialog(win.Window, info); err != nil {
			fmt.Fprintf(os.Stderr, "gui: job panicked: %v\n%s\n(failed to show panic dialog: %v)\n", info.Value, info.Stack, err)
		}
	})

	if _, err := setupStatusPage(win, jobRunner); err != nil {
		return err
	}

	win.Window.ShowAll()
	gtk.Main()

	return nil
}
