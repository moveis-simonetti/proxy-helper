//go:build gui

package gui

import (
	"github.com/gotk3/gotk3/gtk"
)

// window is the GUI's shell: the headerbar (profile selector, page
// switcher, master switch) and the four-page stack. Only the Status page
// carries content in this phase — StatusPage is the container the next
// task fills in. Perfis, Daemon and Importar are placeholders ("Em breve")
// so navigation exists before their own plans build them.
type window struct {
	Window       *gtk.Window
	Stack        *gtk.Stack
	ProfileBtn   *gtk.Button
	MasterSwitch *gtk.Switch
	StatusLabel  *gtk.Label
	StatusPage   *gtk.Box
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

	// The profile selector. Its dropdown behaviour and the list of real
	// profiles belong to the Perfis page's own plan; here it only needs to
	// look right. "Nenhum perfil" is deliberately NOT "Direto"/"Ativo" —
	// those words are reserved for the master switch's state, and reusing
	// one here would make the header read "Direto … Direto" and tell the
	// user nothing. Left insensitive since selecting a profile isn't wired
	// up yet.
	profileBtn, err := gtk.ButtonNewWithLabel("Nenhum perfil")
	if err != nil {
		return nil, err
	}
	profileBtn.SetSensitive(false)
	hb.PackStart(profileBtn)

	// The master switch. Turning the proxy on/off is a later task; leave it
	// visibly inert rather than wiring a half-behaviour. The label is
	// derived from the switch's own (default, inactive) state via
	// masterLabel so the two never disagree.
	masterSwitch, err := gtk.SwitchNew()
	if err != nil {
		return nil, err
	}
	masterSwitch.SetSensitive(false)

	statusLabel, err := gtk.LabelNew(masterLabel(masterSwitch.GetActive()))
	if err != nil {
		return nil, err
	}
	statusLabel.SetTooltipText(masterLabel(masterSwitch.GetActive()))

	switchBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 6)
	if err != nil {
		return nil, err
	}
	switchBox.PackStart(statusLabel, false, false, 0)
	switchBox.PackStart(masterSwitch, false, false, 0)
	hb.PackEnd(switchBox)

	// The four-page stack. Only Status gets real content, in the next task;
	// the others are placeholders that keep navigation honest. Page names
	// are internal identifiers, not user-facing text, so they stay English
	// even though the titles (shown in the switcher) are Portuguese.
	stack, err := gtk.StackNew()
	if err != nil {
		return nil, err
	}

	statusPage, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 6)
	if err != nil {
		return nil, err
	}
	stack.AddTitled(statusPage, "status", "Status")

	placeholders := []struct{ name, title string }{
		{"profiles", "Perfis"},
		{"daemon", "Daemon"},
		{"import", "Importar"},
	}
	for _, p := range placeholders {
		lbl, err := gtk.LabelNew("Em breve")
		if err != nil {
			return nil, err
		}
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
		ProfileBtn:   profileBtn,
		MasterSwitch: masterSwitch,
		StatusLabel:  statusLabel,
		StatusPage:   statusPage,
	}, nil
}
