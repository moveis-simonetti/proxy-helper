//go:build gui

package gui

import (
	"fmt"
	"strings"

	"proxy-helper/internal/app"

	"github.com/gotk3/gotk3/gtk"
)

// showPanicDialog surfaces a job's panic (see runner.setPanicHandler in
// jobs.go) to the user instead of letting it disappear into stderr, which
// is where it would otherwise go unnoticed on a released GUI build with no
// terminal attached. info.Value and info.Stack come straight from the
// recover() inside runner.run — the stack is English/Go internals, so it is
// shown verbatim rather than translated.
func showPanicDialog(win *gtk.Window, info jobPanic) error {
	text := fmt.Sprintf("%v\n\n%s", info.Value, info.Stack)
	dlg, err := gtk.DialogNew()
	if err != nil {
		return err
	}
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	dlg.SetTitle("Erro interno")
	dlg.SetDefaultSize(640, 480)
	dlg.AddButton("Fechar", gtk.RESPONSE_CLOSE)

	content, err := dlg.GetContentArea()
	if err != nil {
		return err
	}
	padDialogContent(content)

	msg, err := gtk.LabelNew("Uma operação falhou de forma inesperada. " +
		"O que já foi aplicado permanece como está; nada foi desfeito.")
	if err != nil {
		return err
	}
	msg.SetXAlign(0)
	msg.SetLineWrap(true)
	content.PackStart(msg, false, false, 0)

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return err
	}
	scroller.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)

	tv, err := gtk.TextViewNew()
	if err != nil {
		return err
	}
	tv.SetMonospace(true)
	tv.SetEditable(false)

	buf, err := tv.GetBuffer()
	if err != nil {
		return err
	}
	buf.SetText(text)

	scroller.Add(tv)
	content.PackStart(scroller, true, true, 0)

	dlg.ShowAll()
	dlg.Run()
	return nil
}

// showPreviewDialog displays a dry run's captured Executor output in a
// monospaced, non-editable TextView inside a ScrolledWindow. text is
// whatever the Executor{DryRun: true, Out: &buf} call wrote to buf — its
// credentials are already redacted by the Executor (see redactSecrets in
// internal/proxy/executor.go), and this function must not undo that.
func showPreviewDialog(win *gtk.Window, text string) error {
	if text == "" {
		text = "nada para simular"
	}
	return showTextDialog(win, "Prévia (dry-run)", text)
}

// showTextDialog is the shared implementation behind showPreviewDialog and
// showApplyResultDialog's restart-result popup: a monospaced, non-editable,
// scrollable text view with a single "Fechar" button.
func showTextDialog(win *gtk.Window, title, text string) error {
	dlg, err := gtk.DialogNew()
	if err != nil {
		return err
	}
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	dlg.SetTitle(title)
	dlg.SetDefaultSize(640, 480)
	dlg.AddButton("Fechar", gtk.RESPONSE_CLOSE)

	content, err := dlg.GetContentArea()
	if err != nil {
		return err
	}
	padDialogContent(content)

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return err
	}
	scroller.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)

	tv, err := gtk.TextViewNew()
	if err != nil {
		return err
	}
	tv.SetMonospace(true)
	tv.SetEditable(false)

	buf, err := tv.GetBuffer()
	if err != nil {
		return err
	}
	buf.SetText(text)

	scroller.Add(tv)
	content.PackStart(scroller, true, true, 0)

	dlg.ShowAll()
	dlg.Run()
	return nil
}

// showApplyResultDialog is the single dialog that replaces the three
// (result, notices, privileged output) that used to open one after another
// for every Apply/Clear. Sections appear in order — per-target results,
// notices, elevated output — and any section with nothing to show is simply
// left out, never packed empty.
//
// rep is nil when no user-level target was selected (a privileged-only
// Apply/Clear): the result and notices sections are skipped, and the dialog
// falls back to a generic title showing only the elevated output.
//
// privOut is the elevated process's raw captured stdout/stderr (see
// applyPrivileged/clearPrivileged in page_status.go); when non-empty it is
// shown inside a gtk.Expander labelled "Saída do processo elevado", closed
// by default — most Applies never need to be read, so it starts out of the
// way instead of dominating the dialog. When privOut is empty the expander
// is not created at all, rather than created-then-hidden: a hidden widget
// built with SetNoShowAll/SetVisible(false) would need an explicit ShowAll
// on its children when later revealed, since plain ShowAll() does not
// descend into a no-show-all widget — simply never adding the widget avoids
// that trap entirely.
//
// onRestartDocker, when non-nil, is wired to the "Reiniciar Docker…" button
// that appears next to the NoticeDockerNeedsRestart notice, if the report
// carries one. It is called only after the user confirms in a secondary
// dialog (see confirmRestartDocker); restartDockerAction (below) is what
// page_status.go and headerbar.go actually pass in.
// privMsg is the one-line outcome of the elevated call ("privilegiados:
// autorização cancelada, nada mudou", a failure, or a plain success). It is
// the ONLY channel for that outcome — the per-target rows below come from
// rep, which covers the in-process targets alone — so it leads the dialog
// instead of being echoed under the buttons on the page.
func showApplyResultDialog(win *gtk.Window, rep *app.Report, privMsg, privOut string, onRestartDocker func()) error {
	dlg, err := gtk.DialogNew()
	if err != nil {
		return err
	}
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	// Modal: onBusy(false) re-enables the action buttons via glib.IdleAdd
	// (jobs.go), which fires *inside* this dialog's nested Run() main loop,
	// not after it returns. Without SetModal the main window stays
	// interactive while the dialog is still open, so the user can queue
	// another Apply/Clear behind it — confusing, even though the runner
	// serializes jobs and nothing actually corrupts.
	dlg.SetModal(true)
	dlg.SetTitle("Resultado")
	dlg.SetDefaultSize(560, 480)
	dlg.AddButton("Fechar", gtk.RESPONSE_CLOSE)

	content, err := dlg.GetContentArea()
	if err != nil {
		return err
	}
	padDialogContent(content)

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return err
	}
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	// spaceSection between the three sections (results, notices, elevated
	// output): they are distinct groups, one step looser than spaceRelated,
	// which is reserved for items within a section — the same 2:1
	// hierarchy the individual sections themselves use below.
	outer, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceSection)
	if err != nil {
		return err
	}

	if privMsg != "" {
		privLbl, err := gtk.LabelNew(privMsg)
		if err != nil {
			return err
		}
		privLbl.SetXAlign(0)
		privLbl.SetLineWrap(true)
		outer.PackStart(privLbl, false, false, 0)
	}

	if rep != nil && len(rep.Results) > 0 {
		resultsBox, err := buildResultsSection(rep)
		if err != nil {
			return err
		}
		outer.PackStart(resultsBox, false, false, 0)
	}

	if rep != nil && len(rep.Notices) > 0 {
		// dlg.Window, not win: the confirmation should stack on top of this
		// result dialog (already transient for win), so it reads as part of
		// the same flow instead of a second top-level dialog.
		noticesBox, err := buildNoticesSection(&dlg.Window, rep, onRestartDocker)
		if err != nil {
			return err
		}
		outer.PackStart(noticesBox, false, false, 0)
	}

	if privOut != "" {
		expander, err := buildPrivilegedOutputExpander(privOut)
		if err != nil {
			return err
		}
		outer.PackStart(expander, false, false, 0)
	}

	scroller.Add(outer)
	content.PackStart(scroller, true, true, 0)

	dlg.ShowAll()
	dlg.Run()
	return nil
}

// buildResultsSection renders one row per Result in rep: applied, cleared,
// skipped or failed, with the skip reason (Result.Detail) and the failure
// text (Result.Err, English, monospaced, visually separated from the
// surrounding Portuguese prose) for the targets that need it. It always
// states that a partial failure is not rolled back, matching the CLI's own
// behaviour: Apply/Clear keep going after one target fails, and nothing
// already applied/cleared is undone.
func buildResultsSection(rep *app.Report) (*gtk.Box, error) {
	// spaceRelated between rows: each row is one target's outcome, so this
	// is the gap between distinct list items, not between lines within one
	// item — it must read as looser than rowBox's spaceTight below, or nothing
	// visually separates "next line of this row" from "next row". Using the
	// same value for both (an earlier version of this file did) collapses
	// the list into one undifferentiated block.
	list, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceRelated)
	if err != nil {
		return nil, err
	}

	for _, row := range resultRows(rep) {
		// spaceTight between the header/detail/error lines within one row:
		// the "inside a group" half of the 2:1 spaceTight/spaceRelated
		// ratio above.
		rowBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
		if err != nil {
			return nil, err
		}

		hdr, err := gtk.LabelNew("")
		if err != nil {
			return nil, err
		}
		hdr.SetXAlign(0)
		hdr.SetMarkup(fmt.Sprintf("<b>%s</b> — %s", gtkEscape(row.Target), gtkEscape(row.Label)))
		rowBox.PackStart(hdr, false, false, 0)

		if row.Detail != "" {
			detail, err := gtk.LabelNew(row.Detail)
			if err != nil {
				return nil, err
			}
			detail.SetXAlign(0)
			detail.SetLineWrap(true)
			rowBox.PackStart(detail, false, false, 0)
		}

		if row.Err != "" {
			errLbl, err := gtk.LabelNew("")
			if err != nil {
				return nil, err
			}
			errLbl.SetXAlign(0)
			errLbl.SetLineWrap(true)
			errLbl.SetMarkup(fmt.Sprintf(`<tt>%s</tt>`, gtkEscape(row.Err)))
			rowBox.PackStart(errLbl, false, false, 0)
		}

		list.PackStart(rowBox, false, false, 0)

		sep, err := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
		if err != nil {
			return nil, err
		}
		list.PackStart(sep, false, false, 0)
	}

	note, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	note.SetXAlign(0)
	note.SetLineWrap(true)
	note.SetMarkup("Nada é desfeito automaticamente: os alvos já aplicados/removidos " +
		"continuam como estão, mesmo quando outro falha.")
	list.PackStart(note, false, false, 0)

	return list, nil
}

// buildNoticesSection renders the warnings raised during an operation
// (unreachable password, Docker pointing at the loopback address, a target
// about to prompt for sudo, Docker needing a restart), one per Notice in
// rep.Notices. Each line is built by noticeText from the Notice's Kind —
// never from text internal/app produced, since that package deliberately
// emits values, not prose, so the CLI can stay English while the GUI speaks
// Portuguese.
//
// The NoticeDockerNeedsRestart notice additionally gets a right-aligned
// "Reiniciar Docker…" button beneath its text, when onRestartDocker is
// non-nil — never built-then-hidden for the other notices, so there is
// nothing here that ShowAll's no-show-all gap could bite.
func buildNoticesSection(win *gtk.Window, rep *app.Report, onRestartDocker func()) (*gtk.Box, error) {
	// spaceRelated between notices: they are items of one list, same 2:1
	// reasoning as the result section's row list above — each notice is a
	// distinct entry, not a line within a shared item, so it gets the
	// wider of the two steps.
	list, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceRelated)
	if err != nil {
		return nil, err
	}

	for _, n := range rep.Notices {
		// spaceTight between a notice's text and its (optional) action
		// button: the "inside one item" half of the ratio.
		item, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, spaceTight)
		if err != nil {
			return nil, err
		}

		lbl, err := gtk.LabelNew(noticeText(n))
		if err != nil {
			return nil, err
		}
		lbl.SetXAlign(0)
		lbl.SetLineWrap(true)
		item.PackStart(lbl, false, false, 0)

		if n.Kind == app.NoticeDockerNeedsRestart && onRestartDocker != nil {
			btnRow, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
			if err != nil {
				return nil, err
			}
			btnRow.SetHAlign(gtk.ALIGN_END)

			restartBtn, err := gtk.ButtonNewWithLabel("Reiniciar Docker…")
			if err != nil {
				return nil, err
			}
			restartBtn.Connect("clicked", func() {
				confirmRestartDocker(win, onRestartDocker)
			})
			btnRow.PackStart(restartBtn, false, false, 0)
			item.PackStart(btnRow, false, false, 0)
		}

		list.PackStart(item, false, false, 0)
	}

	return list, nil
}

// buildPrivilegedOutputExpander wraps the elevated CLI's captured
// stdout/stderr (see applyPrivileged/clearPrivileged in page_status.go) in a
// closed-by-default gtk.Expander labelled "Saída do processo elevado". It
// exists because the GUI cannot build a real per-target Result for
// privileged targets — they run through a reinvoked "proxy set"/"proxy
// unset" under pkexec, not app.Apply/app.Clear — so the CLI's own printed
// table is the only honest account of what happened to each one.
func buildPrivilegedOutputExpander(privOut string) (*gtk.Expander, error) {
	expander, err := gtk.ExpanderNew("Saída do processo elevado")
	if err != nil {
		return nil, err
	}
	expander.SetExpanded(false)

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return nil, err
	}
	scroller.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroller.SetMinContentHeight(160)

	tv, err := gtk.TextViewNew()
	if err != nil {
		return nil, err
	}
	tv.SetMonospace(true)
	tv.SetEditable(false)

	buf, err := tv.GetBuffer()
	if err != nil {
		return nil, err
	}
	buf.SetText(collapseRepeatedLines(privOut))

	scroller.Add(tv)
	expander.Add(scroller)

	return expander, nil
}

// confirmRestartDocker asks for confirmation before handing off to
// onRestartDocker, using the brief's exact wording. It does no I/O itself:
// onRestartDocker (restartDockerAction, below) is what actually shells out,
// off the UI thread via the runner. Declining, or closing the dialog any
// other way, does nothing.
func confirmRestartDocker(win *gtk.Window, onRestartDocker func()) {
	dlg := gtk.MessageDialogNew(win, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO,
		"%s", "Reiniciar o Docker agora? Os containers em execução serão reiniciados.")
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	resp := dlg.Run()
	if resp == gtk.RESPONSE_YES {
		onRestartDocker()
	}
}

// restartDockerAction builds the onRestartDocker callback showApplyResultDialog
// wires to the "Reiniciar Docker…" button: once confirmed, it runs exactly
// the command NoticeDockerNeedsRestart's own text tells the user to run —
// "pkexec systemctl restart docker" — off the UI thread via r.submit
// (reusing runCmd/classifyExit from elevate.go, so a cancelled polkit prompt
// reads as "nothing happened", not an error), then reports the outcome
// through a small dialog transient for win. Shared by page_status.go's
// apply/clear and headerbar.go's profile-switch result, since both can
// raise NoticeDockerNeedsRestart via app.Apply.
func restartDockerAction(win *gtk.Window, r *runner) func() {
	return func() {
		r.submit(func() func() {
			out, cancelled, err := runCmd([]string{"pkexec", "systemctl", "restart", "docker"})
			return func() {
				var text string
				switch {
				case cancelled:
					text = "Autorização cancelada; o Docker não foi reiniciado."
				case err != nil:
					text = fmt.Sprintf("Falha ao reiniciar o Docker: %s\n\n%s", err, strings.TrimSpace(out))
				default:
					text = "Docker reiniciado."
				}
				// Best-effort: there is nowhere left to surface a failure to
				// open this last, purely informational dialog.
				_ = showTextDialog(win, "Reiniciar Docker", text)
			}
		})
	}
}
