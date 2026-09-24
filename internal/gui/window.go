//go:build gui

package gui

import (
	"github.com/gotk3/gotk3/gtk"
	"proxy-helper/internal/proxy"
)

// window is the GUI's shell: the headerbar (profile selector, page
// switcher, master switch) and the four-page stack. Status, Perfis, Daemon
// and Importar all carry real content — StatusPage, ProfilesPage,
// DaemonPage and ImportPage are the empty containers their respective setup
// functions fill in.
type window struct {
	Window       *gtk.Window
	Stack        *gtk.Stack
	ProfileCombo *gtk.ComboBoxText
	MasterSwitch *gtk.Switch
	// AutoCheck is the "auto" lock beside the switch.
	AutoCheck    *gtk.CheckButton
	StatusLabel  *gtk.Label
	StatusPage   *gtk.Box
	ProfilesPage *gtk.Box
	DaemonPage   *gtk.Box
	ImportPage   *gtk.Box

	// App is the GtkApplication whose loop is running. It is what "quit"
	// has to go through since the move to GtkApplication: gtk.MainQuit only
	// stops a gtk.Main() loop, and there is none any more — the tray's
	// "Sair" silently stopped working because of exactly that.
	App *gtk.Application

	// Tray is nil until app.go's Run creates the indicator (newTray needs
	// the window to already exist, to wire "Abrir proxy-helper"). Left nil
	// on a desktop with no tray. The delete-event handler below reads it
	// through the tray's nil-safe methods, never by checking it directly,
	// so it works correctly in both windows: before Tray is set, and on a
	// desktop where it is permanently nil.
	Tray *tray
}

// newWindow builds the window shell and wires its destroy handler to drain
// the runner before quitting the GTK main loop. It does not show the
// window; the caller decides when to call ShowAll.
func newWindow(r *runner) (*window, error) {
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		return nil, err
	}
	win.SetTitle("proxy-helper")
	// The window's own icon, shown by the task bar and the alt-tab switcher.
	// SetIconName, not a file: the name resolves through the icon theme, so
	// the package's installed icon is picked up and a build without it just
	// falls back to the desktop's default rather than failing.
	win.SetIconName(appIconName)
	win.SetDefaultSize(1000, 720)

	hb, err := gtk.HeaderBarNew()
	if err != nil {
		return nil, err
	}
	hb.SetShowCloseButton(true)
	// SetCustomTitle (below) replaces the headerbar's own title/subtitle
	// display with the page switcher, but GTK docs recommend setting the
	// title anyway: it is what some window managers fall back to (window
	// list, alt-tab) when the custom title widget isn't applicable there.
	hb.SetTitle("proxy-helper")

	// The profile selector. Populated and wired up by setupHeaderbar
	// (headerbar.go), called right after newWindow returns — here it only
	// needs to exist and look right empty. "Nenhum perfil" (the label
	// setupHeaderbar shows when ActiveProfile is empty) is deliberately NOT
	// "Direto"/"Ativo" — those words are reserved for the master switch's
	// state, and reusing one here would make the header read "Direto …
	// Direto" and tell the user nothing.
	profileCombo, err := gtk.ComboBoxTextNew()
	if err != nil {
		return nil, err
	}
	profileCombo.SetSensitive(false)
	// Caps the combo's width so a long profile name does not grow it wide
	// enough to push the page switcher (the headerbar's centered custom
	// title widget) off to the side.
	profileCombo.SetSizeRequest(160, -1)
	hb.PackStart(profileCombo)

	// The master switch. Wired up by setupHeaderbar right after newWindow
	// returns; here it only needs to exist and default to off. The label is
	// derived from the switch's own (default, inactive) state via
	// masterLabel so the two never disagree.
	masterSwitch, err := gtk.SwitchNew()
	if err != nil {
		return nil, err
	}

	statusLabel, err := gtk.LabelNew(modeLabel(proxy.ModeDirect, false))
	if err != nil {
		return nil, err
	}

	// The lock is what lets two widgets carry three modes. With it on the
	// switch stops being an input and becomes a readout of what the daemon
	// is actually doing — see modeSwitchState.
	autoCheck, err := gtk.CheckButtonNewWithLabel("auto")
	if err != nil {
		return nil, err
	}

	// 0 here is deliberate, not a stray literal: BoxNew's spacing argument is
	// only a convenience for the common case, and this box sets its real
	// spacing explicitly right below via SetSpacing(spaceTight) instead.
	switchBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, err
	}
	switchBox.SetSpacing(spaceTight)
	switchBox.PackStart(autoCheck, false, false, 0)
	switchBox.PackStart(statusLabel, false, false, 0)
	switchBox.PackStart(masterSwitch, false, false, 0)
	hb.PackEnd(switchBox)

	// The four-page stack. Status and Perfis get real content, filled in by
	// their own setup functions right after newWindow returns; Daemon and
	// Importar are still placeholders that keep navigation honest. Page
	// names are internal identifiers, not user-facing text, so they stay
	// English even though the titles (shown in the switcher) are Portuguese.
	stack, err := gtk.StackNew()
	if err != nil {
		return nil, err
	}

	statusPage, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	padPage(statusPage)
	stack.AddTitled(statusPage, "status", "Status")

	profilesPage, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	padPage(profilesPage)
	stack.AddTitled(profilesPage, "profiles", "Perfis")

	daemonPage, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	padPage(daemonPage)
	stack.AddTitled(daemonPage, "daemon", "Daemon")

	importPage, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	padPage(importPage)
	stack.AddTitled(importPage, "import", "Importar")

	switcher, err := gtk.StackSwitcherNew()
	if err != nil {
		return nil, err
	}
	switcher.SetStack(stack)
	hb.SetCustomTitle(switcher)

	win.SetTitlebar(hb)
	win.Add(stack)

	// The runner must be stopped before the main loop quits: Run() returns
	// right after gtk.Main(), and Go does not wait for stray goroutines. A
	// job in flight would otherwise try to deliver onto a dead UI, or worse,
	// the process would exit mid-Apply with some targets configured and
	// some not.
	win.Connect("destroy", func() {
		r.stop()
	})

	w := &window{
		Window:       win,
		Stack:        stack,
		ProfileCombo: profileCombo,
		MasterSwitch: masterSwitch,
		AutoCheck:    autoCheck,
		StatusLabel:  statusLabel,
		StatusPage:   statusPage,
		ProfilesPage: profilesPage,
		DaemonPage:   daemonPage,
		ImportPage:   importPage,
	}

	// "delete-event" fires on the window-manager close (the titlebar X,
	// Alt+F4, ...) before "destroy" would. In GTK3 this signal returns
	// gboolean, and TRUE CANCELS the close — confirmed by probing
	// handleDeleteEvent directly, not by memory, since getting this
	// backwards makes the window either impossible to close or impossible
	// to hide.
	win.Connect("delete-event", w.handleDeleteEvent)

	return w, nil
}

// handleDeleteEvent is the "delete-event" callback, pulled out as a method
// so it can be called directly in a probe/test without going through GTK's
// signal machinery. Returning true here (and hiding instead of letting
// "destroy" run) only happens when there IS a tray to bring the window back
// from AND the user opted into that via the "Fechar esconde na bandeja"
// item; both checks go through tray's nil-safe methods, so a tray-less
// desktop (w.Tray == nil, read here only after app.go's Run has had a
// chance to set it) always falls through to false and the pre-existing
// "destroy" path runs unchanged. That fallthrough is the guard against the
// worst failure mode this task defines: a window that hides with no icon
// left to reopen it from.
func (w *window) handleDeleteEvent() bool {
	if w.Tray.hasIndicator() && w.Tray.closeToTray() {
		w.Window.Hide()
		return true
	}
	return false
}

// quit ends the application for real. Nil-safe on App so a window built
// outside Run (a probe, a test) still terminates instead of panicking, and
// gtk.MainQuit stays as the fallback for that case alone.
func (w *window) quit() {
	if w == nil {
		return
	}
	if w.App != nil {
		w.App.Quit()
		return
	}
	gtk.MainQuit()
}
