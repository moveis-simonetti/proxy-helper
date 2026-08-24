package gui

import "proxy-helper/internal/proxy"

// statusRow is the display form of one target's state: what the Status
// table shows, independent of any widget. Keeping this pure (no GTK, no
// I/O) is what lets the four-state mapping be covered by go test ./...
// instead of only by eyeballing the window.
type statusRow struct {
	Name string
	// Label is the Portuguese, user-facing word for the state: "ativo",
	// "inativo", "requer sudo" or "indisponível".
	Label string
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
		row.Color = "#c0c0c0"
		row.Selectable = false
	case st.NeedsElevation:
		row.Label = "requer sudo"
		row.Color = "#c64600"
		row.Selectable = true
	case st.Enabled:
		row.Label = "ativo"
		row.Color = "#26a269"
		row.Selectable = true
	default:
		row.Label = "inativo"
		row.Color = "#8b8e8f"
		row.Selectable = true
	}

	return row
}
