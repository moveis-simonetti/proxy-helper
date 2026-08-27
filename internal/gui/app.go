//go:build gui

// Package gui implements the GTK3 graphical interface for proxy-helper.
// Everything in this package requires cgo and GTK3 development headers, and
// is only reachable when the binary is built with the "gui" build tag.
package gui

import (
	"fmt"
	"os"

	"proxy-helper/internal/proxy"

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
// hidden starts the window in the tray instead of on screen, for the
// autostart entry: something that launches at every login should not throw a
// window in the user's face while they are still logging in.
func Run(hidden bool) error {
	// GTK blocks indefinitely trying to open a display connection when
	// neither X11 nor Wayland is available (e.g. a plain SSH session) —
	// it neither errors out nor prints anything, it just hangs. Fail fast
	// with a clear message instead of leaving the user with a frozen
	// terminal they have to Ctrl+C.
	if !DisplayAvailable() {
		return fmt.Errorf("no graphical session found (DISPLAY and WAYLAND_DISPLAY are both unset); the \"gui\" command requires a desktop session — if you're connected over SSH, reconnect with `ssh -X` or `ssh -Y`, or use the other proxy-helper commands instead")
	}

	// BEFORE gtk.Init, not after: GTK derives the X11 WM_CLASS (and the
	// Wayland app_id) from the program name, which defaults to the BINARY'S
	// FILENAME — verified with xprop, where a binary named "ph-gui"
	// produced WM_CLASS ("ph-gui", "Ph-gui"). The desktop entry's
	// StartupWMClass must match, or the launcher does not group with the
	// running window and the taskbar grows a second, unnamed entry.
	//
	// Setting it after gtk.Init fixes only the first half: GDK snapshots
	// the capitalised class field during init, so the pair came out
	// ("proxy-helper-gui", "Ph-gui") — also confirmed with xprop.
	glib.SetPrgname(desktopWMClass)

	gtk.Init(nil)

	// GtkApplication is what makes a second launch raise the running window
	// instead of starting a second copy. It claims appID on the session
	// bus; a later process with the same ID hands its launch to the primary
	// instance as an "activate" and then exits on its own.
	//
	// The tray is what made the missing version of this a real bug and hid
	// it at the same time: with the window tucked away, clicking the menu
	// entry produced a whole second process — a second tray icon, a second
	// set of pages, two writers for one config file — and nothing on screen
	// said so.
	application, err := gtk.ApplicationNew(appID, glib.APPLICATION_FLAGS_NONE)
	if err != nil {
		return err
	}

	// The runner outlives the UI build, so it is created and stopped here
	// rather than inside buildUI: buildUI now returns as soon as the window
	// exists, and a defer in there would stop the worker while the
	// application is still running.
	//
	// deliver wraps glib.IdleAdd in a func() with no return value: IdleAdd
	// accepts a func() bool too, and a stray true there would reschedule the
	// callback forever and pin the UI at 100% CPU.
	jobRunner := newRunner(func(f func()) { glib.IdleAdd(f) })
	jobRunner.start()
	// stop() is idempotent, so this is safe alongside the window's own
	// "destroy" handler calling it too. Without it, Go would not wait for
	// the worker and the process could die with some proxy targets
	// configured and others not.
	defer jobRunner.stop()

	var mainWin *gtk.Window
	var buildErr error
	application.Connect("activate", func() {
		// Every launch after the first arrives here, on the process that
		// already owns the UI. Rebuilding for those would be the duplicate
		// instance bug wearing a different hat; presenting is the whole
		// point.
		if mainWin != nil {
			mainWin.Present()
			return
		}
		mainWin, buildErr = buildUI(application, jobRunner, hidden)
	})

	// nil, not os.Args: GApplication parses what it is given, and it has no
	// idea what "gui --hidden" means — the flag is Cobra's, already consumed
	// before Run is called.
	application.Run(nil)
	return buildErr
}

// buildUI constructs the window, its pages and the tray, and returns the
// window so a later activation can present it. Called once, from the
// application's "activate" handler.
func buildUI(application *gtk.Application, jobRunner *runner, hidden bool) (*gtk.Window, error) {
	win, err := newWindow(jobRunner)
	if err != nil {
		return nil, err
	}
	win.App = application

	// closeToTray starts false on any read failure (missing/corrupt
	// config.json): the safe default is "closing quits", never "closing
	// hides with no way back". LoadProfiles itself already returns an
	// empty ProfileFile rather than an error for the common case (no
	// config.json yet), so this only guards against a genuinely unreadable
	// file.
	closeToTray := false
	if pf, err := proxy.LoadProfiles(); err == nil {
		closeToTray = pf.CloseToTray
	}
	// newTray returning (nil, nil) is an environment fact, not an error: a
	// desktop with no tray (no libayatana, no indicator extension) is a
	// supported place to run this program, so a nil tray is not reported
	// or treated specially here beyond being assigned as-is — window's
	// delete-event handler and every tray method already tolerate it.
	trayInd, err := newTray(win, jobRunner, closeToTray)
	if err != nil {
		return nil, err
	}
	win.Tray = trayInd

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

	statusPg, err := setupStatusPage(win, jobRunner)
	if err != nil {
		return nil, err
	}

	profilesPg, err := setupProfilesPage(win, jobRunner)
	if err != nil {
		return nil, err
	}

	daemonPg, err := setupDaemonPage(win, jobRunner)
	if err != nil {
		return nil, err
	}

	importPg, err := setupImportPage(win, jobRunner)
	if err != nil {
		return nil, err
	}

	headerbar, err := setupHeaderbar(win, jobRunner)
	if err != nil {
		return nil, err
	}
	// Wired here, not inside setupHeaderbar/setupProfilesPage/
	// setupImportPage themselves: app.go is the one place that has all four
	// pages in hand at once. Switching profile via the selector must
	// refresh what the Status page AND the Daemon page's "Perfil" row show;
	// saving/removing a profile on the Perfis page, or importing one from a
	// PAC, must refresh what the selector offers.
	//
	// onProfileChanged takes no arguments and has exactly one call site
	// (syncAfterMasterChange/doEnable in headerbar.go), so two subscribers
	// are just two calls in sequence here — no need for a slice or an event
	// bus for what is, so far, always exactly two listeners.
	headerbar.onProfileChanged = func() {
		statusPg.load()
		daemonPg.load()
		trayInd.refresh()
	}
	profilesPg.onProfilesChanged = func() {
		headerbar.refresh()
		trayInd.refresh()
	}
	// importPg saves a new (never active) profile, so it does not need the
	// load()-both callback above (statusPg/daemonPg have nothing of their
	// own to re-read for a profile that was not already active) — but it
	// DOES need both headerbar.refresh (the selector's list gains an
	// entry) and profilesPg.load (the Perfis tab's own list, which is a
	// separate list from the selector's and does not update on its own).
	// Without the second call, importing a profile and switching to Perfis
	// showed a config.json that already had the new profile but a list
	// that did not, until something else (any save/remove on that page)
	// happened to reload it. trayInd.refresh() keeps the tray's own
	// "Perfil" submenu (a third, independent list) equally in sync.
	importPg.onProfilesChanged = func() {
		headerbar.refresh()
		profilesPg.load()
		trayInd.refresh()
	}
	// trayInd.onChanged is wired here, not inside newTray, because it is
	// the toggle/profile-switch menu actions' own path back into the same
	// sync chain as the headerbar's master switch and selector
	// (headerbarCtl.syncAfterMasterChange): re-read config.json for the
	// selector, then re-run the load()-both callback above (which itself
	// refreshes the tray). newTray runs before setupHeaderbar in this
	// function, so headerbar does not exist yet at that point — this is
	// the earliest place both trayInd and headerbar are in hand together.
	// Safe to call from a menu action even though onProfileChanged is
	// captured by reference here: nothing can activate the tray menu
	// before Run finishes this wiring and reaches gtk.Main().
	trayInd.setOnChanged(func() {
		headerbar.refresh()
		headerbar.onProfileChanged()
	})
	headerbar.refresh()

	// AddWindow ties the window's lifetime to the application: without it
	// GApplication has no window to keep it alive and Run returns
	// immediately.
	application.AddWindow(win.Window)

	// ShowAll first even when starting hidden: it is what marks every child
	// widget visible, so a later Present() from the tray shows a populated
	// window instead of an empty shell.
	win.Window.ShowAll()
	// The guard that matters: with no tray there is no icon to bring the
	// window back from, so starting hidden would leave a running process
	// with no interface and no way to reach it. Fall back to showing it —
	// the same reasoning as window.go's delete-event handler.
	if hidden && trayInd.hasIndicator() {
		win.Window.Hide()
	}
	return win.Window, nil
}
