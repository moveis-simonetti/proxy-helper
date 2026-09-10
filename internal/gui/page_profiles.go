//go:build gui

package gui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/gotk3/gotk3/gtk"
)

// profileSchemes lists the schemes offered by the Esquema combo, in the
// order shown. It mirrors what proxy.Config.URL and the CLI's --scheme flag
// accept.
var profileSchemes = []string{"http", "https", "socks5"}

// renameNotSupportedTooltip explains why the Nome field is locked while
// editing. The CLI has no "profile rename": renaming would be remove+add
// under the hood, and giving the field a text box that looks editable but
// silently does nothing on save would be worse than not offering it. See
// the task brief's "Decisão registrada" for the reasoning this encodes.
const renameNotSupportedTooltip = "Para renomear, crie um novo perfil e remova o antigo."

// profilesPage owns the Perfis page's widgets and its refresh cycle: a
// gtk.ListBox of saved profiles on the left, and an editing form on the
// right. Selecting a row loads that profile into the form; "Novo" clears it
// for a fresh one. All config.json I/O happens inside r.submit, off the GTK
// main loop's thread — see jobs.go's runner and this file's load/save/
// doRemove for where that boundary sits.
type profilesPage struct {
	runner    *runner
	topWindow *gtk.Window

	list *gtk.ListBox
	// names mirrors the list's current rows, in the same order: names[i] is
	// the profile behind row i. gtk.ListBoxRow only ever hands back its
	// index (GetIndex), not an application payload, so this is what
	// resolves a row-selected signal back to a profile name.
	names []string
	// selectedIndex is the index into names of the row backing the form's
	// current contents, or -1 when the form represents a new, unselected
	// profile. GTK has no "undo the last selection" primitive, so
	// onRowSelected tracks this by hand to restore it when the user
	// declines to discard unsaved changes.
	selectedIndex int
	// restoringSelection is true while onRowSelected is putting the
	// selection back after a declined discard. SelectRow/UnselectAll below
	// fire "row-selected" like any other selection change — without this
	// guard, restoring the previous row would reenter onRowSelected,
	// re-run the same dirty check against a form that has not changed, and
	// (since it is not dirty against itself) proceed to reload that row,
	// discarding the guard's own purpose. Same pattern as headerbar.go's
	// repopulating.
	restoringSelection bool

	formTitleLbl *gtk.Label
	// mode and baseline are the state formTitle/isDirty/canSave (all in
	// profileform.go) are pure functions of. baseline is only ever set
	// right after the entries are (re)filled — see fillForm and startNew —
	// never before: setting it first would let each field's "changed"
	// during the fill compare against a stale baseline and light up Salvar
	// on a mere click.
	mode     formMode
	baseline profileFormValues
	// busy mirrors the runner's busy state, kept in sync by the
	// setBusyHandler callback below. refreshActionState reads it because the
	// entries themselves are NOT disabled during a job (disabling all seven
	// on every load() — which runs after every save — would flash the form
	// for no benefit), so a keystroke mid-job would otherwise re-enable
	// Salvar via the field's own "changed" handler and let a second write
	// queue up behind the one still running.
	busy bool

	nameEntry    *gtk.Entry
	schemeCombo  *gtk.ComboBoxText
	hostEntry    *gtk.Entry
	portEntry    *gtk.Entry
	userEntry    *gtk.Entry
	passEntry    *gtk.Entry
	noProxyEntry *gtk.Entry

	// newBtn is the list pane's "+ Novo perfil" button now, not a control in
	// the form's own action bar — it moved so creating a profile has an
	// entry point next to the list it will add a row to.
	newBtn *gtk.Button
	// saveBtn is the form's primary action button. Its label switches
	// between primaryActionLabel(modeNew) and primaryActionLabel(modeEdit),
	// so the field keeps its long-standing name even though it now also
	// covers "Criar perfil".
	saveBtn *gtk.Button
	// cancelBtn undoes unsaved edits without writing anything — see cancel().
	cancelBtn *gtk.Button
	removeBtn *gtk.Button
	resultLbl *gtk.Label

	// globalNoProxyEntry and globalNoProxyLbl belong to the "No-proxy
	// global" frame below the form, not to the per-profile form above —
	// kept as separate fields so save()/startNew() never touch them.
	globalNoProxyEntry    *gtk.Entry
	globalNoProxySaveBtn  *gtk.Button
	globalNoProxyResetBtn *gtk.Button
	globalNoProxyLbl      *gtk.Label

	// editing is the name of the profile currently loaded into the form, or
	// "" when the form represents a new, unsaved profile. It is what
	// validateProfileForm needs to allow saving a profile under its own
	// existing name, and what confirmRemove/doRemove act on.
	editing string

	// onProfilesChanged is called whenever this page writes config.json in
	// a way that changes which profiles exist (save, remove) — wired from
	// app.go to the headerbar's refresh, so the profile selector's list
	// stays in sync with what this page just did. nil until app.go wires
	// it up right after both pages are set up; save/doRemove guard the
	// call accordingly.
	onProfilesChanged func()
}

// setupProfilesPage fills win.ProfilesPage (built empty by newWindow) with
// the page's layout: a two-column body — the profile list on the left, the
// editing form on the right — and a global no-proxy panel spanning the
// width below, left empty here for Task 3 to fill in.
func setupProfilesPage(win *window, r *runner) (*profilesPage, error) {
	pp := &profilesPage{runner: r, topWindow: win.Window, selectedIndex: -1}

	body, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceRelated)
	if err != nil {
		return nil, err
	}

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return nil, err
	}
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	list, err := gtk.ListBoxNew()
	if err != nil {
		return nil, err
	}
	pp.list = list
	list.Connect("row-selected", func(_ *gtk.ListBox, row *gtk.ListBoxRow) {
		pp.onRowSelected(row)
	})
	scroller.Add(list)

	// listPane stacks the list above its "+ Novo perfil" button — the entry
	// point for creating a profile lives next to the list it will add a row
	// to, which is where a user looking for it actually looks, rather than
	// buried in the form's own action bar (see newBtn below).
	listPane, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	// SetSizeRequest moved here from scroller: listPane is now the panel
	// that must not grow past the list's intended width, since it also
	// contains the button below.
	listPane.SetSizeRequest(220, -1)
	listPane.PackStart(scroller, true, true, 0)

	newBtn, err := gtk.ButtonNewWithLabel("+ Novo perfil")
	if err != nil {
		return nil, err
	}
	newBtn.Connect("clicked", func() { pp.startNew() })
	listPane.PackStart(newBtn, false, false, 0)
	pp.newBtn = newBtn

	// The list pane does not expand: it should take only the width it needs
	// (SetSizeRequest above), leaving the rest of the panel to the form.
	// Without this, a list of two profiles claimed half the window.
	body.PackStart(listPane, false, false, 0)

	form, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return nil, err
	}
	body.PackStart(form, true, true, 0)

	win.ProfilesPage.PackStart(body, true, true, 0)

	// sectionLabels collects every row label across the form's four
	// sections so they can share one gtk.SizeGroup below. Without that,
	// each gtk.Grid sizes its own label column independently and the
	// sections' fields end up starting at four different horizontal
	// positions — a subtler version of the same misalignment this task
	// fixes.
	var sectionLabels []*gtk.Label

	// formTitleLbl is the one control this task is explicitly allowed to
	// add: a "Novo perfil" / "Editando “X”" heading. Today's only cue is the
	// Nome field greying out, which is easy to miss. modeTitle, not
	// sectionTitle: the same bold as "Identificação"/"Servidor" made this
	// read as one more section heading rather than a mode indicator.
	formTitleLbl, err := modeTitle("")
	if err != nil {
		return nil, err
	}
	form.PackStart(formTitleLbl, false, false, 0)
	pp.formTitleLbl = formTitleLbl

	formTitleSep, err := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	if err != nil {
		return nil, err
	}
	formTitleSep.SetMarginTop(spaceTight)
	formTitleSep.SetMarginBottom(spaceSection)
	form.PackStart(formTitleSep, false, false, 0)

	idTitle, err := sectionTitle("Identificação")
	if err != nil {
		return nil, err
	}
	// Unlike the other section titles, this one skips spaceSection's own
	// top margin: formTitleSep above it already carries that gap
	// (SetMarginBottom(spaceSection)), and adding another here would double
	// it.
	form.PackStart(idTitle, false, false, 0)

	idGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	form.PackStart(idGrid, false, false, 0)

	nameEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	pp.nameEntry = nameEntry
	nameLbl, err := addGridRow(idGrid, 0, "Nome", nameEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, nameLbl)

	serverTitle, err := sectionTitle("Servidor")
	if err != nil {
		return nil, err
	}
	serverTitle.ToWidget().SetMarginTop(spaceSection)
	form.PackStart(serverTitle, false, false, 0)

	serverGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	form.PackStart(serverGrid, false, false, 0)

	schemeCombo, err := gtk.ComboBoxTextNew()
	if err != nil {
		return nil, err
	}
	for _, s := range profileSchemes {
		// Append(id, text), not AppendText: AppendText leaves the item's ID
		// nil, and fillForm/startNew select with SetActiveID, which matches by
		// ID. With no IDs the selection silently never happens and the combo
		// shows empty for every profile.
		schemeCombo.Append(s, s)
	}
	pp.schemeCombo = schemeCombo
	schemeLbl, err := addGridRow(serverGrid, 0, "Esquema", schemeCombo)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, schemeLbl)

	hostEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	pp.hostEntry = hostEntry
	hostLbl, err := addGridRow(serverGrid, 1, "Host", hostEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, hostLbl)

	portEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	pp.portEntry = portEntry
	portLbl, err := addGridRow(serverGrid, 2, "Porta", portEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, portLbl)

	credsTitle, err := sectionTitle("Credenciais")
	if err != nil {
		return nil, err
	}
	credsTitle.ToWidget().SetMarginTop(spaceSection)
	form.PackStart(credsTitle, false, false, 0)

	credsGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	form.PackStart(credsGrid, false, false, 0)

	userEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	pp.userEntry = userEntry
	userLbl, err := addGridRow(credsGrid, 0, "Usuário", userEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, userLbl)

	passEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	passEntry.SetVisibility(false)
	pp.passEntry = passEntry
	passLbl, err := addGridRow(credsGrid, 1, "Senha", passEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, passLbl)

	exceptionsTitle, err := sectionTitle("Exceções")
	if err != nil {
		return nil, err
	}
	exceptionsTitle.ToWidget().SetMarginTop(spaceSection)
	form.PackStart(exceptionsTitle, false, false, 0)

	exceptionsGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	form.PackStart(exceptionsGrid, false, false, 0)

	noProxyEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	pp.noProxyEntry = noProxyEntry
	noProxyLbl, err := addGridRow(exceptionsGrid, 0, "No-proxy do perfil", noProxyEntry)
	if err != nil {
		return nil, err
	}
	sectionLabels = append(sectionLabels, noProxyLbl)

	// Every field feeds refreshActionState, which recomputes canSave against
	// pp.baseline. fillForm's SetText calls below fire "changed" too — see
	// its own comment for why baseline must always be set after the fields,
	// never before, so these early recalculations land on stale-but-safe
	// zero values instead of lighting up Salvar on a mere click.
	nameEntry.Connect("changed", func() { pp.refreshActionState() })
	schemeCombo.Connect("changed", func() { pp.refreshActionState() })
	hostEntry.Connect("changed", func() { pp.refreshActionState() })
	portEntry.Connect("changed", func() { pp.refreshActionState() })
	userEntry.Connect("changed", func() { pp.refreshActionState() })
	passEntry.Connect("changed", func() { pp.refreshActionState() })
	noProxyEntry.Connect("changed", func() { pp.refreshActionState() })

	labelGroup, err := gtk.SizeGroupNew(gtk.SIZE_GROUP_HORIZONTAL)
	if err != nil {
		return nil, err
	}
	for _, lbl := range sectionLabels {
		labelGroup.AddWidget(lbl)
	}

	buttons, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
	if err != nil {
		return nil, err
	}
	// ALIGN_FILL + HExpand, not ALIGN_END: with ALIGN_END the box shrinks to
	// fit its children and PackStart(removeBtn) has nothing to separate it
	// from, so it never actually reaches the left edge.
	buttons.SetHAlign(gtk.ALIGN_FILL)
	buttons.SetHExpand(true)
	buttons.SetMarginTop(spaceSection)

	removeBtn, err := gtk.ButtonNewWithLabel("Remover")
	if err != nil {
		return nil, err
	}
	// Packed at the start, apart from Cancelar/primary at the end: it is
	// destructive, and the GNOME HIG keeps a destructive action away from
	// the primary one so it is not clicked by accident reaching for it.
	//
	// Hidden (not merely insensitive) outside edit mode: in modeNew there is
	// nothing to remove, and a dead button sitting there is noise. Because
	// it is packed at the start and the other two at the end, hiding it does
	// not shift them.
	//
	// SetNoShowAll(true) BEFORE SetVisible(false): app.go's Run calls
	// win.Window.ShowAll() once, after every page is set up — including
	// this one, already in modeNew — and gtk_widget_show_all forces every
	// descendant visible regardless of an earlier SetVisible(false),
	// silently undoing it. Excluding this widget from that recursive show
	// is what makes SetVisible below (here and in startNew/fillForm/save)
	// the actual, lasting say over whether it is shown.
	removeBtn.SetNoShowAll(true)
	removeBtn.SetVisible(false)
	removeBtn.Connect("clicked", func() { pp.confirmRemove() })
	buttons.PackStart(removeBtn, false, false, 0)
	pp.removeBtn = removeBtn

	cancelBtn, err := gtk.ButtonNewWithLabel("Cancelar")
	if err != nil {
		return nil, err
	}
	cancelBtn.SetSensitive(false)
	cancelBtn.Connect("clicked", func() { pp.cancel() })
	buttons.PackEnd(cancelBtn, false, false, 0)
	pp.cancelBtn = cancelBtn

	// primaryBtn's label is set by startNew/fillForm/save via
	// primaryActionLabel(pp.mode); the empty string here is replaced before
	// the window is ever shown (setupProfilesPage calls startNew below).
	// Packed end LAST: PackEnd stacks each new call closer to the true end
	// than the ones before it, so packing this after cancelBtn is what puts
	// it in the primary action's conventional rightmost position.
	primaryBtn, err := gtk.ButtonNewWithLabel("")
	if err != nil {
		return nil, err
	}
	primaryBtn.Connect("clicked", func() { pp.save() })
	buttons.PackEnd(primaryBtn, false, false, 0)
	pp.saveBtn = primaryBtn

	form.PackStart(buttons, false, false, 0)

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	form.PackStart(resultLbl, false, false, 0)
	pp.resultLbl = resultLbl

	noProxyFrame, err := gtk.FrameNew("No-proxy global")
	if err != nil {
		return nil, err
	}
	win.ProfilesPage.PackStart(noProxyFrame, false, false, 0)

	if err := pp.setupGlobalNoProxyFrame(noProxyFrame); err != nil {
		return nil, err
	}

	// Disable the buttons that submit a job while one is already running —
	// same reasoning as the Status page's busy handler (page_status.go):
	// the runner is one-job-at-a-time, and a click landing mid-save would
	// just queue up behind it with a stale view of the form.
	r.setBusyHandler(func(busy bool) {
		// pp.busy is set BEFORE refreshActionState() below, not after: that
		// call reads pp.busy to decide Salvar/Cancelar's state, so recording
		// it late would make the recalculation act on the previous job's
		// state.
		pp.busy = busy
		enabled := !busy
		pp.newBtn.SetSensitive(enabled)
		// Always goes through refreshActionState, busy or not: it is the one
		// place that knows both "is there a job in flight" (pp.busy) and
		// "does the form actually have unsaved changes" (canSave/canCancel).
		// A blanket SetSensitive(true) here would resurrect Salvar/Cancelar
		// after a finished job (e.g. load(), which runs after every save)
		// even for a form that is not dirty.
		pp.refreshActionState()
		pp.removeBtn.SetSensitive(enabled)
		pp.globalNoProxySaveBtn.SetSensitive(enabled)
		pp.globalNoProxyResetBtn.SetSensitive(enabled)
	})

	// Returning to the Perfis tab lands on "Novo perfil" again. Without this
	// the page is initialised exactly once, at startup, so leaving mid-edit
	// and coming back later still showed "Editando: X" — the tab remembered
	// a state the user had visually left behind.
	//
	// Skipped while the form is dirty: silently clearing unsaved work would
	// be a back door around the discard confirmation this page already asks
	// for elsewhere. A tab switch is not a decision to throw work away.
	// Skipped while busy for the same reason a job disables the buttons.
	win.Stack.Connect("notify::visible-child", func() {
		if win.Stack.GetVisibleChildName() != "profiles" {
			return
		}
		if pp.busy || isDirty(pp.baseline, pp.readForm()) {
			return
		}
		pp.startNew()
		// Reloads the list from disk on every visit, the same call
		// setupProfilesPage makes right after its own startNew() below —
		// what makes this tab self-sufficient: config.json can change from
		// outside this page entirely (the Importar page, the CLI in
		// another window, a text editor), and entering the tab is what
		// notices, rather than depending on whichever page changed it
		// remembering to call back in. Placed after startNew(), matching
		// setup's order, though the two do not interact here: startNew()
		// only touches the form, load() only touches the list/selection.
		pp.load()
	})

	pp.startNew()
	pp.load()

	return pp, nil
}

// globalNoProxySaveTooltip warns about the trap described in the task
// brief: EffectiveGlobalNoProxy (internal/proxy/profiles.go) tests
// GlobalNoProxy != nil, not len == 0, so nil and an empty slice are NOT the
// same thing to it — nil falls back to DefaultGlobalNoProxy, an empty slice
// means "no host bypasses the proxy". Clearing the box and saving lands on
// the nil branch (splitNoProxy("") never allocates), so in practice it
// restores the default rather than turning the list off. Without this
// tooltip a user who empties the field and saves would see the default
// list reappear and reasonably conclude the GUI ignored what they typed.
const globalNoProxySaveTooltip = "Uma lista vazia não desativa o no-proxy global: ela restaura os valores padrão (host.docker.internal, localhost, 127.0.0.1)."

// setupGlobalNoProxyFrame fills frame with the global no-proxy editor: an
// Entry holding the comma-separated list (mirroring the per-profile field
// and "proxy config set --no-proxy"), an explanatory dimmed label, and
// Salvar/Restaurar padrão buttons. The Entry's initial content is filled
// later by applyGlobalNoProxy, once load() has read config.json.
func (pp *profilesPage) setupGlobalNoProxyFrame(frame *gtk.Frame) error {
	box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
	if err != nil {
		return err
	}
	// The frame draws a border; without an inner margin the entry and the
	// buttons sit flush against it, which is what made this block look
	// cramped next to the padded sections above.
	box.SetMarginTop(spaceRelated)
	box.SetMarginBottom(spaceRelated)
	box.SetMarginStart(spaceRelated)
	box.SetMarginEnd(spaceRelated)
	frame.Add(box)

	// Bold label, matching the four section headings above: a plain frame
	// label reads as a different kind of thing for no reason, when this is
	// just one more titled group on the same page.
	if title, err := sectionTitle("No-proxy global"); err == nil {
		frame.SetLabelWidget(title)
	}

	entry, err := gtk.EntryNew()
	if err != nil {
		return err
	}
	box.PackStart(entry, false, false, 0)
	pp.globalNoProxyEntry = entry

	explainLbl, err := gtk.LabelNew("Aplicado a todos os perfis e a \"Aplicar\" avulso.")
	if err != nil {
		return err
	}
	explainLbl.SetXAlign(0)
	if ctx, err := explainLbl.GetStyleContext(); err == nil {
		ctx.AddClass("dim-label")
	}
	box.PackStart(explainLbl, false, false, 0)

	buttons, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
	if err != nil {
		return err
	}

	saveBtn, err := gtk.ButtonNewWithLabel("Salvar")
	if err != nil {
		return err
	}
	saveBtn.SetTooltipText(globalNoProxySaveTooltip)
	saveBtn.Connect("clicked", func() { pp.saveGlobalNoProxy() })
	buttons.PackStart(saveBtn, false, false, 0)
	pp.globalNoProxySaveBtn = saveBtn

	resetBtn, err := gtk.ButtonNewWithLabel("Restaurar padrão")
	if err != nil {
		return err
	}
	resetBtn.Connect("clicked", func() { pp.resetGlobalNoProxy() })
	buttons.PackStart(resetBtn, false, false, 0)
	pp.globalNoProxyResetBtn = resetBtn

	// Right-aligned and set apart from the hint above: the form on this same
	// page puts its actions at the end of the line, and having these two go
	// left made the block look unfinished, with the whole right half empty.
	buttons.SetHAlign(gtk.ALIGN_END)
	buttons.SetMarginTop(spaceRelated)
	box.PackStart(buttons, false, false, 0)

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	box.PackStart(resultLbl, false, false, 0)
	pp.globalNoProxyLbl = resultLbl

	return nil
}

// saveGlobalNoProxy reads the Entry on the UI thread, then writes
// pf.GlobalNoProxy under WithProfileLock off it. See globalNoProxySaveTooltip
// for why an emptied box does not clear the list but resets it to default.
func (pp *profilesPage) saveGlobalNoProxy() {
	text := entryText(pp.globalNoProxyEntry)

	pp.runner.submit(func() func() {
		// app.SetGlobalNoProxy, not a bare WithProfileLock: with
		// --via-local the daemon holds the list that actually decides
		// where traffic goes, so a write nobody reloads leaves the
		// setting saved on disk and inert in practice.
		effective, err := app.SetGlobalNoProxy(
			statusDeps(),
			&proxy.Executor{Escalation: proxy.EscalateNone},
			splitNoProxy(text),
		)
		return func() {
			if err != nil {
				pp.globalNoProxyLbl.SetText(fmt.Sprintf("erro ao salvar no-proxy global: %s", err))
				return
			}
			pp.globalNoProxyEntry.SetText(strings.Join(effective, ", "))
			pp.globalNoProxyLbl.SetText("no-proxy global salvo")
		}
	})
}

// resetGlobalNoProxy clears pf.GlobalNoProxy under WithProfileLock — nil,
// not an empty non-nil slice, so EffectiveGlobalNoProxy falls back to
// DefaultGlobalNoProxy — then repopulates the Entry with that default.
func (pp *profilesPage) resetGlobalNoProxy() {
	pp.runner.submit(func() func() {
		effective, err := app.SetGlobalNoProxy(
			statusDeps(),
			&proxy.Executor{Escalation: proxy.EscalateNone},
			nil,
		)
		return func() {
			if err != nil {
				pp.globalNoProxyLbl.SetText(fmt.Sprintf("erro ao restaurar no-proxy global: %s", err))
				return
			}
			pp.globalNoProxyEntry.SetText(strings.Join(effective, ", "))
			pp.globalNoProxyLbl.SetText("no-proxy global restaurado ao padrão")
		}
	})
}

// applyGlobalNoProxy renders pf's effective global no-proxy list into the
// Entry. Runs on the UI thread only, called from applyProfiles once load()
// has read config.json — the same delivery load() already uses for the
// profile list, so both parts of the page stay in sync with what is on
// disk.
func (pp *profilesPage) applyGlobalNoProxy(pf *proxy.ProfileFile) {
	pp.globalNoProxyEntry.SetText(strings.Join(pf.EffectiveGlobalNoProxy(), ", "))
}

// entryText reads an Entry's current text. GetText returns an error only
// when the underlying widget is invalid, which cannot happen for a live
// *gtk.Entry built by this file; treating that case as empty text is
// simpler than propagating an error nothing can act on.
func entryText(e *gtk.Entry) string {
	s, err := e.GetText()
	if err != nil {
		return ""
	}
	return s
}

// startNew clears the form for a fresh, unsaved profile: editing becomes
// "", the Nome field becomes editable again, and any list selection is
// dropped. Called both from the list pane's "+ Novo perfil" button and once
// at setup, so the page starts in this same state.
func (pp *profilesPage) startNew() {
	pp.editing = ""
	pp.nameEntry.SetText("")
	pp.nameEntry.SetSensitive(true)
	pp.nameEntry.SetTooltipText("")
	pp.schemeCombo.SetActiveID(profileSchemes[0])
	pp.hostEntry.SetText("")
	pp.portEntry.SetText("")
	pp.userEntry.SetText("")
	pp.passEntry.SetText("")
	pp.noProxyEntry.SetText("")
	pp.selectedIndex = -1
	// UnselectAll fires "row-selected" with a nil row, which onRowSelected
	// already returns on immediately — no restoringSelection guard needed
	// here, unlike the declined-discard path below.
	pp.list.UnselectAll()
	// Hidden, not just insensitive: modeNew has nothing to remove. See its
	// SetVisible(false) at setup for why hiding it (packed at the start)
	// does not disturb Cancelar/primary (packed at the end).
	pp.removeBtn.SetVisible(false)
	pp.resultLbl.SetText("")

	// mode/baseline are set after the fields above are cleared, not
	// before — see the profilesPage doc comment on baseline for why.
	pp.mode = modeNew
	// readForm(), not a zero profileFormValues: the scheme combo above was
	// just set to a default, so a blank new form is NOT the zero struct.
	// Baselining the zero struct made an untouched form look dirty, which
	// popped "Descartar este novo perfil?" on the first row click and left
	// Salvar lit with nothing to save.
	pp.baseline = pp.readForm()
	setModeTitle(pp.formTitleLbl, formTitle(modeNew, ""))
	pp.saveBtn.SetLabel(primaryActionLabel(modeNew))
	pp.refreshActionState()
}

// cancel undoes unsaved edits in the form, per the mode currently active.
// Purely local: unlike Salvar/Remover, it never calls a proxy.* command —
// see the task brief's Global Constraints for why.
//
// In modeEdit this reloads the profile from disk and repopulates the form
// with it — startEdit does exactly that (load, then fillForm) — so the
// selected row and pp.editing are untouched: the user stays on the same
// profile, just with their unsaved typing thrown away. In modeNew there is
// nothing on disk to reload, so it just calls startNew().
func (pp *profilesPage) cancel() {
	if pp.mode == modeNew {
		pp.startNew()
		return
	}
	pp.startEdit(pp.editing)
}

// readForm captures the form's current contents as a profileFormValues,
// exactly as save() builds vals — used both to write and, here, to compare
// against pp.baseline for isDirty/canSave. Safe to call from the UI thread
// only (it reads widgets directly, no I/O).
func (pp *profilesPage) readForm() profileFormValues {
	return profileFormValues{
		Name:    strings.TrimSpace(entryText(pp.nameEntry)),
		Scheme:  pp.schemeCombo.GetActiveText(),
		Host:    strings.TrimSpace(entryText(pp.hostEntry)),
		Port:    strings.TrimSpace(entryText(pp.portEntry)),
		User:    entryText(pp.userEntry),
		Pass:    entryText(pp.passEntry),
		NoProxy: entryText(pp.noProxyEntry),
	}
}

// refreshActionState recomputes whether the primary button and Cancelar
// have anything to do, per canSave/canCancel (profileform.go). Connected to
// every field's "changed" signal, so it also fires (harmlessly) while
// fillForm/startNew are still populating the form — see their comments for
// why baseline is set only after that.
func (pp *profilesPage) refreshActionState() {
	// While a job is in flight both buttons stay off no matter what the
	// form says: the entries are not disabled during a job, so a keystroke
	// would otherwise re-enable them and let a second write (or a cancel
	// racing a save) queue up behind the job still running, acting on
	// values it never saw.
	if pp.busy {
		pp.saveBtn.SetSensitive(false)
		pp.cancelBtn.SetSensitive(false)
		return
	}
	current := pp.readForm()
	pp.saveBtn.SetSensitive(canSave(pp.baseline, current))
	pp.cancelBtn.SetSensitive(canCancel(pp.baseline, current))
}

// confirmDiscardChanges asks the user before an unsaved edit is thrown away
// by loading a different profile into the form. Returns true when there is
// nothing to lose, or the user confirms discarding it.
func (pp *profilesPage) confirmDiscardChanges() bool {
	if !isDirty(pp.baseline, pp.readForm()) {
		return true
	}

	var msg string
	if pp.mode == modeNew {
		msg = "Descartar este novo perfil?"
	} else {
		msg = fmt.Sprintf("Descartar as alterações não salvas em %q?", pp.editing)
	}

	dlg := gtk.MessageDialogNew(pp.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO, "%s", msg)
	dlg.SetModal(true)
	dlg.SetTransientFor(pp.topWindow)
	resp := dlg.Run()
	dlg.Destroy()
	return resp == gtk.RESPONSE_YES
}

// onRowSelected reacts to the list's "row-selected" signal. row is nil when
// the selection is cleared (e.g. by startNew's UnselectAll), in which case
// there is nothing to load.
func (pp *profilesPage) onRowSelected(row *gtk.ListBoxRow) {
	// restoringSelection guards against reentrancy — see its doc comment on
	// profilesPage.
	if pp.restoringSelection {
		return
	}
	if row == nil {
		return
	}
	idx := row.GetIndex()
	if idx < 0 || idx >= len(pp.names) || idx == pp.selectedIndex {
		return
	}

	if !pp.confirmDiscardChanges() {
		// Put the selection back where it was, under the guard: SelectRow/
		// UnselectAll themselves fire "row-selected" and would otherwise
		// reenter this handler.
		pp.restoringSelection = true
		if pp.selectedIndex >= 0 {
			if prevRow := pp.list.GetRowAtIndex(pp.selectedIndex); prevRow != nil {
				pp.list.SelectRow(prevRow)
			}
		} else {
			pp.list.UnselectAll()
		}
		pp.restoringSelection = false
		return
	}

	pp.selectedIndex = idx
	pp.startEdit(pp.names[idx])
}

// startEdit loads name's saved Config into the form, off the UI thread:
// reading config.json is I/O and so cannot run on the signal handler that
// triggered this (see the package doc comment on I/O placement).
func (pp *profilesPage) startEdit(name string) {
	pp.runner.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return func() { pp.resultLbl.SetText(fmt.Sprintf("erro ao carregar perfil: %s", err)) }
		}
		cfg, ok := pf.Get(name)
		if !ok {
			return func() { pp.resultLbl.SetText(fmt.Sprintf("perfil %q não encontrado", name)) }
		}
		return func() { pp.fillForm(name, cfg) }
	})
}

// fillForm renders cfg into the form and marks it as editing name. The
// Nome field is made insensitive: the CLI has no "profile rename", so
// changing it here would either need to be silently ignored on save or
// invent a rename the CLI cannot perform. See renameNotSupportedTooltip.
func (pp *profilesPage) fillForm(name string, cfg proxy.Config) {
	pp.editing = name
	pp.nameEntry.SetText(name)
	pp.nameEntry.SetSensitive(false)
	pp.nameEntry.SetTooltipText(renameNotSupportedTooltip)

	scheme := cfg.Scheme
	if scheme == "" {
		scheme = "http"
	}
	pp.schemeCombo.SetActiveID(scheme)

	pp.hostEntry.SetText(cfg.Host)
	pp.portEntry.SetText(cfg.Port)
	pp.userEntry.SetText(cfg.Username)
	pp.passEntry.SetText(cfg.Password)
	pp.noProxyEntry.SetText(strings.Join(cfg.NoProxy, ", "))
	pp.removeBtn.SetVisible(true)
	pp.removeBtn.SetSensitive(true)
	pp.resultLbl.SetText("")

	// mode/baseline are set after every SetText above, not before: each one
	// fires "changed" (see the profilesPage doc comment on baseline), and
	// setting baseline first would make the first of those seven compare
	// against a stale value from whatever was in the form before — lighting
	// up Salvar and the "unsaved changes" prompt from a plain row click.
	pp.mode = modeEdit
	pp.baseline = pp.readForm()
	setModeTitle(pp.formTitleLbl, formTitle(modeEdit, name))
	pp.saveBtn.SetLabel(primaryActionLabel(modeEdit))
	pp.refreshActionState()
}

// save reads the form and validates+writes it inside a single
// WithProfileLock call, off the UI thread. The form fields are read here,
// on the UI thread, and captured in vals before submit — reading a widget
// from the runner's worker goroutine is exactly the bug the Status page
// already fixed once (see this file's package doc comment and apply() in
// page_status.go for the same pattern).
func (pp *profilesPage) save() {
	vals := pp.readForm()
	editing := pp.editing

	pp.runner.submit(func() func() {
		var errMsg string
		// app.SaveProfile, not a bare WithProfileLock: editing the ACTIVE
		// profile has to reach a running daemon, which resolves that
		// profile's host, port, credentials and no-proxy itself. A write
		// nobody reloads looks saved and does nothing.
		//
		// The validation runs inside SaveProfile's guard, which is called
		// under the same lock as the write — reading the existing profiles
		// is I/O, and doing it there keeps the duplicate-name check atomic
		// with the write rather than advisory.
		err := app.SaveProfile(
			statusDeps(),
			&proxy.Executor{Escalation: proxy.EscalateNone},
			func(existing map[string]proxy.Config) (string, proxy.Config, error) {
				name, cfg, msg := validateProfileForm(vals, existing, editing)
				if msg != "" {
					errMsg = msg
					return "", proxy.Config{}, app.ErrRejected
				}
				return name, cfg, nil
			},
		)
		// A rejection is not a failure: errMsg already says what is wrong
		// in the user's own language, so drop the sentinel here rather
		// than showing it next to a Portuguese message.
		if errors.Is(err, app.ErrRejected) {
			err = nil
		}

		return func() {
			if err != nil {
				pp.resultLbl.SetText(fmt.Sprintf("erro ao salvar perfil: %s", err))
				return
			}
			if errMsg != "" {
				pp.resultLbl.SetText(errMsg)
				return
			}
			// NOT startNew(): that used to empty the form back to "Novo
			// perfil" right after a successful save, which was the main
			// complaint — editing a profile and saving made it vanish from
			// the screen. Instead, keep it on screen as a saved, no-longer-
			// dirty edit of itself.
			pp.editing = vals.Name
			pp.mode = modeEdit
			pp.baseline = vals
			pp.nameEntry.SetSensitive(false)
			pp.nameEntry.SetTooltipText(renameNotSupportedTooltip)
			setModeTitle(pp.formTitleLbl, formTitle(modeEdit, vals.Name))
			pp.saveBtn.SetLabel(primaryActionLabel(modeEdit))
			pp.removeBtn.SetVisible(true)
			pp.removeBtn.SetSensitive(true)
			pp.refreshActionState()
			pp.resultLbl.SetText(fmt.Sprintf("perfil %q salvo", vals.Name))
			pp.load()
			if pp.onProfilesChanged != nil {
				pp.onProfilesChanged()
			}
		}
	})
}

// confirmRemove asks for confirmation before deleting the profile currently
// loaded in the form. The dialog is modal and transient for the main
// window — the Status page's result dialog already had a bug from skipping
// SetModal (see showApplyResultDialog in dialogs.go), so this follows the
// same pattern deliberately.
func (pp *profilesPage) confirmRemove() {
	name := pp.editing
	if name == "" {
		return
	}

	dlg := gtk.MessageDialogNew(pp.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO,
		"Remover o perfil %q?", name)
	dlg.SetModal(true)
	dlg.SetTransientFor(pp.topWindow)
	resp := dlg.Run()
	dlg.Destroy()
	if resp != gtk.RESPONSE_YES {
		return
	}
	pp.doRemove(name)
}

// doRemove deletes name inside WithProfileLock, clearing ActiveProfile/
// LastProfile when they point at it — a dangling last_profile makes
// "proxy on" fail with a name the user can no longer see, exactly as
// cmd/proxy_profile.go's remove command documents. If the removed profile
// was active, the daemon is reloaded afterwards, deliberately outside the
// lock: flock does not nest within this process, and ReloadDaemon must
// never run while WithProfileLock still holds the file lock.
func (pp *profilesPage) doRemove(name string) {
	pp.runner.submit(func() func() {
		var found, wasActive bool
		err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			if _, exists := pf.Get(name); !exists {
				return nil
			}
			found = true
			delete(pf.Profiles, name)
			wasActive = pf.ActiveProfile == name
			if wasActive {
				pf.ActiveProfile = ""
				// See cmd/proxy_profile.go: a forwarding mode with nothing
				// selected is not a state the daemon can honour.
				pf.Mode = string(proxy.ModeDirect)
			}
			if pf.LastProfile == name {
				pf.LastProfile = ""
			}
			return nil
		})
		if err != nil {
			return func() { pp.resultLbl.SetText(fmt.Sprintf("erro ao remover perfil: %s", err)) }
		}
		if !found {
			return func() {
				pp.resultLbl.SetText(fmt.Sprintf("perfil %q não encontrado", name))
				pp.startNew()
				pp.load()
			}
		}

		var reloadErr error
		if wasActive {
			reloadErr = serve.ReloadDaemon(&proxy.Executor{Escalation: proxy.EscalateNone})
		}

		return func() {
			if reloadErr != nil {
				pp.resultLbl.SetText(fmt.Sprintf("perfil %q removido, mas o daemon não recarregou: %s", name, reloadErr))
			} else {
				pp.resultLbl.SetText(fmt.Sprintf("perfil %q removido", name))
			}
			pp.startNew()
			pp.load()
			if pp.onProfilesChanged != nil {
				pp.onProfilesChanged()
			}
		}
	})
}

// load re-reads config.json and repopulates the list, off the UI thread.
// Called at setup and after every save/remove so the list reflects what is
// actually on disk rather than what the form assumes happened.
func (pp *profilesPage) load() {
	pp.runner.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		return func() { pp.applyProfiles(pf, err) }
	})
}

// applyProfiles rebuilds the list's rows from pf. Runs on the UI thread
// only, from a runner delivery.
func (pp *profilesPage) applyProfiles(pf *proxy.ProfileFile, err error) {
	if err != nil {
		pp.resultLbl.SetText(fmt.Sprintf("erro ao carregar perfis: %s", err))
		return
	}

	pp.applyGlobalNoProxy(pf)

	pp.list.GetChildren().Foreach(func(item interface{}) {
		if w, ok := item.(*gtk.Widget); ok {
			pp.list.Remove(w)
		}
	})

	names := visibleProfiles(pf)
	pp.names = names

	for _, name := range names {
		cfg := pf.Profiles[name]

		row, err := gtk.ListBoxRowNew()
		if err != nil {
			pp.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}

		box, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
		if err != nil {
			pp.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}

		title := name
		if name == pf.ActiveProfile {
			title += " — ativo"
		}
		nameLbl, err := gtk.LabelNew(title)
		if err != nil {
			pp.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}
		nameLbl.SetXAlign(0)
		box.PackStart(nameLbl, false, false, 0)

		detailLbl, err := gtk.LabelNew(profileSummary(cfg))
		if err != nil {
			pp.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}
		detailLbl.SetXAlign(0)
		if ctx, err := detailLbl.GetStyleContext(); err == nil {
			ctx.AddClass("dim-label")
		}
		box.PackStart(detailLbl, false, false, 0)

		row.Add(box)
		pp.list.Add(row)
	}

	pp.list.ShowAll()

	// Restore the row for whatever the form is currently showing (set by
	// save() right before calling load(), or by startEdit/fillForm): the
	// list was just rebuilt from scratch, which drops GTK's own selection
	// state, and leaving it unselected here is what used to make a
	// just-saved profile look deselected even though the form still shows
	// it. Guarded like the declined-discard path in onRowSelected: SelectRow
	// fires "row-selected", and the form already matches this profile, so
	// reentering would be redundant at best.
	pp.selectedIndex = -1
	if pp.editing != "" {
		if idx := slices.Index(names, pp.editing); idx >= 0 {
			pp.selectedIndex = idx
			pp.restoringSelection = true
			if row := pp.list.GetRowAtIndex(idx); row != nil {
				pp.list.SelectRow(row)
			}
			pp.restoringSelection = false
		}
	}
}

// profileSummary renders "scheme://host:port" for a list row's dimmed
// second line, omitting the port when unset.
func profileSummary(cfg proxy.Config) string {
	scheme := cfg.Scheme
	if scheme == "" {
		scheme = "http"
	}
	if cfg.Port == "" {
		return fmt.Sprintf("%s://%s", scheme, cfg.Host)
	}
	return fmt.Sprintf("%s://%s:%s", scheme, cfg.Host, cfg.Port)
}
