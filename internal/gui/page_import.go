//go:build gui

package gui

import (
	"errors"
	"fmt"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/gotk3/gotk3/gtk"
)

// importPage owns the Importar page's widgets. It fetches a PAC file's
// proxy list (proxy.FetchPAC, 15s network timeout — always inside
// r.submit, never on the UI thread, see fetch()), lets the user pick one of
// the entries found, and saves it as a new profile via app.SaveProfile —
// the same "resolve under the profile lock" pattern page_profiles.go uses
// for Salvar.
//
// There is deliberately no Aplicar button here: the task brief's control
// table maps this page only to "proxy import --save-profile" (URL +
// Analisar, the entry list, and Salvar como perfil plus user/pass), nothing
// that would need --targets or --dry-run.
type importPage struct {
	runner    *runner
	topWindow *gtk.Window

	urlEntry   *gtk.Entry
	analyzeBtn *gtk.Button
	resultLbl  *gtk.Label
	list       *gtk.ListBox
	warningLbl *gtk.Label
	nameEntry  *gtk.Entry
	userEntry  *gtk.Entry
	passEntry  *gtk.Entry
	saveBtn    *gtk.Button
	saveResult *gtk.Label

	// entries mirrors the list's current rows, in the same order: entries[i]
	// is the PACProxy behind row i. gtk.ListBoxRow only ever hands back its
	// index (GetIndex), the same reasoning as profilesPage.names.
	entries []proxy.PACProxy
	// chosen is the entry currently picked, or the zero proxy.PACProxy{}
	// (validateImportForm's "nothing chosen yet" sentinel) when none is.
	chosen proxy.PACProxy

	// selectingRow guards against reentrancy while applyEntries pre-selects
	// a single entry's row: SelectRow fires "row-selected" like any other
	// selection change, and without this guard that would reenter
	// onRowSelected while entries/list are still being rebuilt. Same
	// pattern as page_profiles.go's restoringSelection and headerbar.go's
	// repopulating.
	selectingRow bool

	// busy mirrors the runner's busy state, kept in sync by the
	// setBusyHandler callback below — see profilesPage.busy's doc comment
	// for why refreshActionState needs it: the fields are not disabled
	// during a job, so a keystroke mid-job would otherwise resurrect Salvar
	// via the entry's own "changed" handler.
	busy bool

	// onProfilesChanged is called after a successful save, wired from
	// app.go to the same refresh the Perfis page's save/remove already
	// trigger (headerbar.refresh, which also feeds the Perfis page's own
	// load()) — see app.go for how the three are chained together. nil
	// until app.go wires it up.
	onProfilesChanged func()
}

// setupImportPage fills win.ImportPage (built empty by newWindow) with the
// page's layout: URL + Analisar, the results list with its warning, and the
// profile form (Nome/Usuário/Senha + Salvar como perfil).
func setupImportPage(win *window, r *runner) (*importPage, error) {
	ip := &importPage{runner: r, topWindow: win.Window}

	title, err := sectionTitle("Importar de um PAC")
	if err != nil {
		return nil, err
	}
	win.ImportPage.PackStart(title, false, false, 0)

	urlGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	win.ImportPage.PackStart(urlGrid, false, false, 0)

	urlRow, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceRelated)
	if err != nil {
		return nil, err
	}

	urlEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	urlEntry.SetHExpand(true)
	urlEntry.SetPlaceholderText("http://proxy.corp/proxy.pac")
	ip.urlEntry = urlEntry
	urlRow.PackStart(urlEntry, true, true, 0)

	analyzeBtn, err := gtk.ButtonNewWithLabel("Analisar")
	if err != nil {
		return nil, err
	}
	analyzeBtn.Connect("clicked", func() { ip.fetch() })
	urlRow.PackStart(analyzeBtn, false, false, 0)
	ip.analyzeBtn = analyzeBtn

	if _, err := addGridRow(urlGrid, 0, "URL", urlRow); err != nil {
		return nil, err
	}

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	win.ImportPage.PackStart(resultLbl, false, false, 0)
	ip.resultLbl = resultLbl

	entriesTitle, err := sectionTitle("Proxies encontrados")
	if err != nil {
		return nil, err
	}
	entriesTitle.ToWidget().SetMarginTop(spaceSection)
	win.ImportPage.PackStart(entriesTitle, false, false, 0)

	list, err := gtk.ListBoxNew()
	if err != nil {
		return nil, err
	}
	// Hidden until fetch() has something to show: an empty list sitting
	// under "Proxies encontrados" before the user ever clicks Analisar
	// would look broken rather than merely unused. SetNoShowAll before
	// SetVisible(false) — ShowAll() (app.go's Run, after every page is set
	// up) forces every descendant visible regardless of an earlier
	// SetVisible(false), so skipping it from that recursive show is what
	// makes SetVisible below the actual, lasting say over whether it shows.
	list.SetNoShowAll(true)
	list.SetVisible(false)
	list.Connect("row-selected", func(_ *gtk.ListBox, row *gtk.ListBoxRow) {
		ip.onRowSelected(row)
	})
	win.ImportPage.PackStart(list, false, false, 0)
	ip.list = list

	warningLbl, err := gtk.LabelNew(pacWarning(2))
	if err != nil {
		return nil, err
	}
	warningLbl.SetXAlign(0)
	warningLbl.SetLineWrap(true)
	if ctx, err := warningLbl.GetStyleContext(); err == nil {
		ctx.AddClass("dim-label")
	}
	// Same reasoning as list above: born hidden, shown only when
	// applyEntries decides two-or-more entries warrant the warning.
	warningLbl.SetNoShowAll(true)
	warningLbl.SetVisible(false)
	win.ImportPage.PackStart(warningLbl, false, false, 0)
	ip.warningLbl = warningLbl

	profileTitle, err := sectionTitle("Perfil")
	if err != nil {
		return nil, err
	}
	profileTitle.ToWidget().SetMarginTop(spaceSection)
	win.ImportPage.PackStart(profileTitle, false, false, 0)

	profileGrid, err := formGrid()
	if err != nil {
		return nil, err
	}
	win.ImportPage.PackStart(profileGrid, false, false, 0)

	nameEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	ip.nameEntry = nameEntry
	if _, err := addGridRow(profileGrid, 0, "Nome", nameEntry); err != nil {
		return nil, err
	}

	userEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	ip.userEntry = userEntry
	if _, err := addGridRow(profileGrid, 1, "Usuário", userEntry); err != nil {
		return nil, err
	}

	passEntry, err := gtk.EntryNew()
	if err != nil {
		return nil, err
	}
	passEntry.SetVisibility(false)
	ip.passEntry = passEntry
	if _, err := addGridRow(profileGrid, 2, "Senha", passEntry); err != nil {
		return nil, err
	}

	nameEntry.Connect("changed", func() { ip.refreshActionState() })
	userEntry.Connect("changed", func() { ip.refreshActionState() })
	passEntry.Connect("changed", func() { ip.refreshActionState() })

	buttons, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
	if err != nil {
		return nil, err
	}
	buttons.SetHAlign(gtk.ALIGN_END)
	buttons.SetMarginTop(spaceRelated)

	saveBtn, err := gtk.ButtonNewWithLabel("Salvar como perfil")
	if err != nil {
		return nil, err
	}
	saveBtn.Connect("clicked", func() { ip.save() })
	buttons.PackEnd(saveBtn, false, false, 0)
	ip.saveBtn = saveBtn

	win.ImportPage.PackStart(buttons, false, false, 0)

	saveResult, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	saveResult.SetXAlign(0)
	saveResult.SetLineWrap(true)
	win.ImportPage.PackStart(saveResult, false, false, 0)
	ip.saveResult = saveResult

	// Disable the buttons that submit a job while one is already running —
	// same reasoning as every other page's busy handler (jobs.go's runner
	// is one-job-at-a-time).
	r.setBusyHandler(func(busy bool) {
		ip.busy = busy
		enabled := !busy
		ip.analyzeBtn.SetSensitive(enabled)
		ip.refreshActionState()
	})

	ip.refreshActionState()

	return ip, nil
}

// fetch reads the URL entry on the UI thread, then runs proxy.FetchPAC —
// real network I/O with a 15s timeout — inside r.submit, off the UI thread.
// Without that, the window would appear frozen for up to 15 seconds on
// every click. While the job runs, setBusyHandler above disables Analisar
// and applyEntries's "Analisando…" text (set here, before submit, since it
// needs no I/O) tells the user something is happening.
func (ip *importPage) fetch() {
	rawURL := entryText(ip.urlEntry)
	if rawURL == "" {
		ip.resultLbl.SetText("Informe a URL do arquivo PAC.")
		return
	}

	ip.resultLbl.SetText("Analisando…")

	ip.runner.submit(func() func() {
		entries, err := proxy.FetchPAC(rawURL)
		return func() { ip.applyEntries(entries, err) }
	})
}

// applyEntries renders a FetchPAC result onto the list, warning label and
// result label. Runs on the UI thread only, from a runner delivery (or, for
// the probe described in the task brief, called directly with in-memory
// entries — it does no I/O of its own).
func (ip *importPage) applyEntries(entries []proxy.PACProxy, err error) {
	if err != nil {
		ip.resultLbl.SetText(fmt.Sprintf("Não foi possível ler o PAC: %s", err))
		ip.entries = nil
		ip.chosen = proxy.PACProxy{}
		ip.clearList()
		ip.list.SetVisible(false)
		ip.warningLbl.SetVisible(false)
		ip.refreshActionState()
		return
	}

	ip.entries = entries
	ip.chosen = proxy.PACProxy{}
	ip.clearList()

	if len(entries) == 0 {
		ip.resultLbl.SetText("Nenhum proxy encontrado nesse arquivo PAC.")
		ip.list.SetVisible(false)
		ip.warningLbl.SetVisible(false)
		ip.refreshActionState()
		return
	}

	ip.resultLbl.SetText("")

	for _, p := range entries {
		row, err := gtk.ListBoxRowNew()
		if err != nil {
			ip.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}
		lbl, err := gtk.LabelNew(pacEntryLabel(p))
		if err != nil {
			ip.resultLbl.SetText(fmt.Sprintf("erro ao montar a lista: %s", err))
			return
		}
		lbl.SetXAlign(0)
		row.Add(lbl)
		ip.list.Add(row)
		// ShowAll on the row, not on the list: the list carries
		// no-show-all (so a window-wide ShowAll cannot reveal it while it
		// is meant to be empty), and gtk_widget_show_all() returns
		// immediately on a widget with that flag WITHOUT descending into
		// its children. Calling it on the list was therefore a no-op, and
		// every row stayed invisible — the list looked empty no matter
		// what the PAC returned. Rows carry no such flag.
		row.ShowAll()
	}
	ip.list.SetVisible(true)

	if len(entries) == 1 {
		// A single entry is pre-selected: with nothing to choose between,
		// forcing a click first would be pointless friction — the CLI does
		// the same, using index 0 without asking for --index. See
		// pacWarning's doc comment for why the warning stays hidden here
		// too.
		ip.selectingRow = true
		if row := ip.list.GetRowAtIndex(0); row != nil {
			ip.list.SelectRow(row)
		}
		ip.selectingRow = false
		ip.chosen = entries[0]
		ip.warningLbl.SetVisible(false)
	} else {
		// Two or more: none is pre-selected, forcing the user to actually
		// choose, and the regex-parsing caveat is shown since ambiguity is
		// now real.
		ip.list.UnselectAll()
		ip.warningLbl.SetText(pacWarning(len(entries)))
		ip.warningLbl.SetVisible(true)
	}

	ip.refreshActionState()
}

// clearList empties the ListBox's rows, the same Foreach/Remove pattern
// page_profiles.go's applyProfiles uses.
func (ip *importPage) clearList() {
	ip.list.GetChildren().Foreach(func(item interface{}) {
		if w, ok := item.(*gtk.Widget); ok {
			ip.list.Remove(w)
		}
	})
}

// onRowSelected reacts to the list's "row-selected" signal, recording which
// PACProxy the user picked. row is nil when the selection is cleared (e.g.
// applyEntries's UnselectAll for a fresh multi-entry result).
func (ip *importPage) onRowSelected(row *gtk.ListBoxRow) {
	if ip.selectingRow {
		return
	}
	if row == nil {
		ip.chosen = proxy.PACProxy{}
		ip.refreshActionState()
		return
	}
	idx := row.GetIndex()
	if idx < 0 || idx >= len(ip.entries) {
		return
	}
	ip.chosen = ip.entries[idx]
	ip.refreshActionState()
}

// refreshActionState recomputes whether Salvar como perfil has anything to
// do: insensitive while busy (same reasoning as profilesPage.busy), or
// without both a chosen entry and a typed name — validateImportForm needs
// both to produce anything savable, and re-running the full validation just
// to enable a button would duplicate its own judgement for no benefit.
func (ip *importPage) refreshActionState() {
	if ip.busy {
		ip.saveBtn.SetSensitive(false)
		return
	}
	hasChosen := ip.chosen != proxy.PACProxy{}
	hasName := entryText(ip.nameEntry) != ""
	ip.saveBtn.SetSensitive(hasChosen && hasName)
}

// save reads the form and the chosen entry on the UI thread, then writes
// under app.SaveProfile's lock off it — the same pattern
// profilesPage.save() uses, and for the same reason: reading a widget from
// the runner's worker goroutine is undefined, and the duplicate-name check
// inside resolve has to run atomically with the write.
func (ip *importPage) save() {
	vals := importFormValues{
		URL:  entryText(ip.urlEntry),
		Name: entryText(ip.nameEntry),
		User: entryText(ip.userEntry),
		Pass: entryText(ip.passEntry),
	}
	chosen := ip.chosen

	ip.runner.submit(func() func() {
		var errMsg string
		var savedName string
		err := app.SaveProfile(
			statusDeps(),
			&proxy.Executor{Escalation: proxy.EscalateNone},
			func(existing map[string]proxy.Config) (string, proxy.Config, error) {
				name, cfg, msg := validateImportForm(vals, chosen, existing)
				if msg != "" {
					errMsg = msg
					return "", proxy.Config{}, app.ErrRejected
				}
				savedName = name
				return name, cfg, nil
			},
		)
		// A rejection is not a failure: errMsg already says what is wrong
		// in the user's own language, same as profilesPage.save().
		if errors.Is(err, app.ErrRejected) {
			err = nil
		}

		return func() {
			if err != nil {
				ip.saveResult.SetText(fmt.Sprintf("erro ao salvar perfil: %s", err))
				return
			}
			if errMsg != "" {
				ip.saveResult.SetText(errMsg)
				return
			}
			// Clear the credential and name fields, but keep the fetched
			// list: the user may want to save a second entry from the same
			// PAC as another profile. Not startNew()-style reset of the
			// URL/list — there is nothing to discard there, unlike
			// profilesPage's form.
			ip.nameEntry.SetText("")
			ip.userEntry.SetText("")
			ip.passEntry.SetText("")
			ip.saveResult.SetText(fmt.Sprintf("Perfil %q criado a partir do PAC.", savedName))
			ip.refreshActionState()
			// Not ReloadDaemon: app.SaveProfile only reloads when the saved
			// profile is the active one, and a freshly created profile
			// never is (see its own doc comment) — nothing else to do here
			// beyond telling the other pages the profile list changed.
			if ip.onProfilesChanged != nil {
				ip.onProfilesChanged()
			}
		}
	})
}
