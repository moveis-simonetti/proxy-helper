//go:build gui

package gui

import (
	"fmt"
	"html"

	"github.com/gotk3/gotk3/gtk"
)

// The spacing scale, from the GNOME HIG. It is deliberately three steps, not
// seven: a wider scale invites picking "whatever looks good" at each call
// site, which is exactly how the UI ended up with no consistent spacing in
// the first place. Every gap in the GUI must be one of these three values —
// if a spot seems to need something in between (9, 15, ...), that is a sign
// the grouping/hierarchy is wrong, not that the scale is missing a step.
const (
	// spaceTight separates a label from its field, or buttons within the
	// same group.
	spaceTight = 6
	// spaceRelated separates rows of a form, or panels placed side by side.
	spaceRelated = 12
	// spaceSection is the page's outer margin, and the gap between titled
	// sections.
	spaceSection = 18
)

// padPage applies the page's outer margin. Every stack page gets this and
// nothing else gets it: it is what keeps content off the window edge.
func padPage(w gtk.IWidget) {
	widget := w.ToWidget()
	widget.SetMarginTop(spaceSection)
	widget.SetMarginBottom(spaceSection)
	widget.SetMarginStart(spaceSection)
	widget.SetMarginEnd(spaceSection)
}

// padDialogContent applies spaceRelated as both the margin around a dialog's
// content area and the spacing between its direct children (the message and
// the scrollable body, say). It is padPage's counterpart for dialogs: a
// dialog's GetContentArea() otherwise ships with no margin at all, so its
// content sits flush against the dialog's edges — the same problem padPage
// solved for the stack's pages, at the dialog scale (spaceRelated, not
// spaceSection, since a dialog is a much smaller container).
func padDialogContent(content *gtk.Box) {
	content.SetSpacing(spaceRelated)
	widget := content.ToWidget()
	widget.SetMarginTop(spaceRelated)
	widget.SetMarginBottom(spaceRelated)
	widget.SetMarginStart(spaceRelated)
	widget.SetMarginEnd(spaceRelated)
}

// sectionTitle builds the bold heading that introduces a group of fields.
func sectionTitle(text string) (*gtk.Label, error) {
	lbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	// html.EscapeString guards against Pango markup parse failures: a title
	// containing "&", "<" or ">" would otherwise break SetMarkup's parser,
	// which logs a GTK warning and falls back to showing the raw markup
	// text to the user instead of the intended heading.
	lbl.SetMarkup(fmt.Sprintf("<b>%s</b>", html.EscapeString(text)))
	lbl.SetXAlign(0)
	return lbl, nil
}

// modeTitle builds the form's mode indicator. It deliberately does NOT reuse
// sectionTitle: the indicator sat in the same bold as "Identificação" and
// "Servidor", so it read as one more section heading and users missed which
// mode they were in. size="large" sets it apart from those.
//
// text is escaped the same way sectionTitle's is, and for the same reason —
// except here it is load-bearing rather than defensive: the edit-mode text
// embeds the profile's name, which the user chose and may contain "&".
func modeTitle(text string) (*gtk.Label, error) {
	lbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	setModeTitle(lbl, text)
	lbl.SetXAlign(0)
	return lbl, nil
}

// setModeTitle updates a label built by modeTitle in place. Callers MUST use
// this instead of SetText for every later update of that label: SetText
// replaces the markup with plain text, silently dropping the
// size="large"/bold styling — the label would keep displaying, but it would
// go back to looking like ordinary text, the exact confusion this indicator
// exists to fix.
func setModeTitle(lbl *gtk.Label, text string) {
	lbl.SetMarkup(fmt.Sprintf(`<span size="large" weight="bold">%s</span>`, html.EscapeString(text)))
}

// formGrid builds the two-column grid a section's rows go into: labels in
// column 0, controls in column 1, with the label column sized to the
// widest label so every control lines up.
func formGrid() (*gtk.Grid, error) {
	grid, err := gtk.GridNew()
	if err != nil {
		return nil, err
	}
	grid.SetRowSpacing(spaceTight)
	grid.SetColumnSpacing(spaceRelated)
	return grid, nil
}

// addGridRow appends one label+control row to a grid built by formGrid.
func addGridRow(g *gtk.Grid, row int, label string, control gtk.IWidget) (*gtk.Label, error) {
	lbl, err := gtk.LabelNew(label)
	if err != nil {
		return nil, err
	}
	// Right-aligned so the label butts up against its field rather than
	// floating in the middle of the (widest-label-sized) column.
	lbl.SetXAlign(1)
	g.Attach(lbl, 0, row, 1, 1)

	control.ToWidget().SetHExpand(true)
	g.Attach(control, 1, row, 1, 1)

	return lbl, nil
}
