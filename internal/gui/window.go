//go:build gui

package gui

import (
	"github.com/gotk3/gotk3/gtk"
)

// window is the GUI's shell: the headerbar (profile selector, page
// switcher, master switch) and the four-page stack. Status and Perfis carry
// real content — StatusPage and ProfilesPage are the empty containers their
// respective setup functions fill in. Daemon and Importar are still
// placeholders ("Em breve") so navigation exists before their own plans
// build them.
type window struct {
	Window       *gtk.Window
	Stack        *gtk.Stack
	ProfileCombo *gtk.ComboBoxText
	MasterSwitch *gtk.Switch
	StatusLabel  *gtk.Label
	StatusPage   *gtk.Box
	ProfilesPage *gtk.Box
}

// masterLabel derives the Portuguese wording for the master switch's label
// from the switch's own state, so the header never shows a label and a
// switch position that contradict each other. It is the single place that
// maps switch state to label text — the task that wires the toggle should
// call this rather than inventing its own mapping.
func masterLabel(active bool) string {
	if active {
		return "Ativo"
	}
	return "Direto"
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

	statusLabel, err := gtk.LabelNew(masterLabel(masterSwitch.GetActive()))
	if err != nil {
		return nil, err
	}
	statusLabel.SetTooltipText(masterLabel(masterSwitch.GetActive()))

	// 0 here is deliberate, not a stray literal: BoxNew's spacing argument is
	// only a convenience for the common case, and this box sets its real
	// spacing explicitly right below via SetSpacing(spaceTight) instead.
	switchBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, err
	}
	switchBox.SetSpacing(spaceTight)
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

	placeholders := []struct{ name, title string }{
		{"daemon", "Daemon"},
		{"import", "Importar"},
	}
	for _, p := range placeholders {
		lbl, err := gtk.LabelNew("Em breve")
		if err != nil {
			return nil, err
		}
		padPage(lbl)
		stack.AddTitled(lbl, p.name, p.title)
	}

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
		gtk.MainQuit()
	})

	return &window{
		Window:       win,
		Stack:        stack,
		ProfileCombo: profileCombo,
		MasterSwitch: masterSwitch,
		StatusLabel:  statusLabel,
		StatusPage:   statusPage,
		ProfilesPage: profilesPage,
	}, nil
}
