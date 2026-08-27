package gui

import (
	"fmt"

	"proxy-helper/internal/proxy"
)

// statusRow is the display form of one target's state: what the Status
// table shows, independent of any widget. Keeping this pure (no GTK, no
// I/O) is what lets the four-state mapping be covered by go test ./...
// instead of only by eyeballing the window.
type statusRow struct {
	Name string
	// Label is the Portuguese, user-facing word for the state: "aplicado",
	// "sem proxy", "requer sudo" or "indisponível".
	//
	// "ativo"/"inativo" were the original words, but "ativo" is also what
	// the headerbar's master switch uses for a different thing entirely
	// (proxy on vs. direct connection) — two unrelated concepts sharing a
	// word in the same window was part of the confusion reported about this
	// page, so the Status table's own state got renamed instead.
	Label string
	// Marker is a one-character glyph shown before Label so the state is
	// recognisable at a glance in a table of eleven rows, without reading
	// every word: "●" aplicado, "○" sem proxy, "▲" requer sudo, "—"
	// indisponível.
	Marker string
	// Color is the CSS/Pango hex color the state label is rendered in.
	Color string
	// Detail carries proxy.Status.Detail through unchanged — the target's
	// own explanation (e.g. "docker not found"), already in English by
	// project convention.
	Detail string
	// Root reports whether the target requires elevated privileges to
	// apply, independent of its current state. The table uses it to show
	// the padlock.
	Root bool
	// Selectable is false only when the target is unavailable: its
	// checkbox is disabled and the row is greyed out, never shown as an
	// error.
	Selectable bool
	// TintWholeRow asks the table to paint the name and the detail in
	// Color too, not just the state word. It is set for the two states the
	// user cannot simply read past — unavailable and needs-elevation — so
	// the row reads as one unit at a glance, instead of making the eye hunt
	// for the single coloured word among eleven rows.
	TintWholeRow bool
}

// rowFor maps a target's raw proxy.Status into its display form. The four
// states are checked in the order the project's status table documents
// them: unavailable first (never an error, always wins over any other
// signal), then needing elevation, then enabled, and inactive as the
// fallback. root is the target's RequiresRoot(), passed in separately
// because proxy.Status carries no notion of it.
func rowFor(st proxy.Status, root bool) statusRow {
	row := statusRow{
		Name:   st.Name,
		Detail: st.Detail,
		Root:   root,
	}

	switch {
	case !st.Available:
		row.Label = "indisponível"
		row.Marker = "—"
		row.Color = "#c0c0c0"
		row.Selectable = false
		row.TintWholeRow = true
	case st.NeedsElevation:
		row.Label = "requer sudo"
		row.Marker = "▲"
		row.Color = "#c64600"
		row.Selectable = true
		// Tinted like the unavailable rows: "cannot be read" is a state
		// the user has to act on, and a lone coloured word was easy to
		// miss in a table of eleven targets.
		row.TintWholeRow = true
	case st.Enabled:
		row.Label = "aplicado"
		row.Marker = "●"
		row.Color = "#26a269"
		row.Selectable = true
	default:
		row.Label = "sem proxy"
		row.Marker = "○"
		row.Color = "#8b8e8f"
		row.Selectable = true
	}

	return row
}

// elevationBarText renders the Status page's "read with sudo" bar message
// for n targets that need elevation, in Portuguese, with correct singular
// and plural (note the verb changes too, not just the noun). Pure logic, no
// GTK, so it is testable without a display — see rows_test.go.
func elevationBarText(n int) string {
	if n == 1 {
		return "1 alvo não pode ser lido sem sudo."
	}
	return fmt.Sprintf("%d alvos não podem ser lidos sem sudo.", n)
}

// summaryText is the single line under the table. It reports the counts
// separately on purpose: a target whose state could not be read is neither
// applied nor unapplied, and folding it into either would state something the
// program does not know. That is exactly what "requer sudo" means — the read
// failed, not that the proxy is absent.
//
// locked is how many selected targets will raise a password prompt. It used
// to be its own sentence in its own strip ("Aplicar vai pedir sua senha (3
// alvos com cadeado)"), which put two competing texts beside the buttons and
// pushed them onto a second line. It is a property of the selection like the
// other three, so it reads as one more segment of the same sentence.
func summaryText(selected, total, applied, unknown, locked int) string {
	s := fmt.Sprintf("%d de %d alvos selecionados", selected, total)
	if applied > 0 {
		s += fmt.Sprintf(" · %d já aplicados", applied)
	}
	if unknown > 0 {
		s += fmt.Sprintf(" · %d não verificados", unknown)
	}
	switch {
	case locked == 1:
		s += " · \U0001F512 1 pede senha"
	case locked > 1:
		s += fmt.Sprintf(" · \U0001F512 %d pedem senha", locked)
	}
	return s
}

// promptsForPassword reports whether applying this target will raise a
// password prompt: it needs root AND is not session-scoped. A session-scoped
// target (gnome, kde) never goes through the pkexec path regardless of
// RequiresRoot — see splitSessionAware — so it costs no password.
//
// It exists as one predicate because the Status page has two call sites that
// must agree: the padlock glyph on a row, and the count in the "Aplicar vai
// pedir sua senha" hint. They disagreed — the glyph excluded session-scoped
// targets and the count did not — so on a machine where gnome's
// RequiresRoot is true the footer claimed four padlocks over three.
func promptsForPassword(root, sessionScoped bool) bool {
	return root && !sessionScoped
}
