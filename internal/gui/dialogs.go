//go:build gui

package gui

import (
	"fmt"

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
	content.SetSpacing(6)

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

// showPrivilegedOutputDialog displays the elevated CLI's captured
// stdout/stderr for the privileged targets (see applyPrivileged/
// clearPrivileged in page_status.go). It exists because the GUI cannot
// build a real per-target Result for those targets — they run through a
// reinvoked "proxy set"/"proxy unset" under pkexec, not app.Apply/
// app.Clear — so the CLI's own printed table is the only honest account of
// what happened to each one.
func showPrivilegedOutputDialog(win *gtk.Window, text string) error {
	if text == "" {
		text = "nenhuma saída capturada"
	} else {
		text = collapseRepeatedLines(text)
	}
	return showTextDialog(win, "Resultado (privilegiados)", text)
}

// showTextDialog is the shared implementation behind showPreviewDialog and
// showPrivilegedOutputDialog: a monospaced, non-editable, scrollable text
// view with a single "Fechar" button.
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
	content.SetSpacing(6)

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

// showResultDialog displays what happened to each target in rep: applied,
// cleared, skipped or failed, with the skip reason (Result.Detail) and the
// failure text (Result.Err, English, monospaced, visually separated from
// the surrounding Portuguese prose) for the targets that need it. It always
// states that a partial failure is not rolled back, matching the CLI's own
// behaviour: Apply/Clear keep going after one target fails, and nothing
// already applied/cleared is undone.
func showResultDialog(win *gtk.Window, rep *app.Report) error {
	dlg, err := gtk.DialogNew()
	if err != nil {
		return err
	}
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	dlg.SetTitle("Resultado")
	dlg.SetDefaultSize(560, 420)
	dlg.AddButton("Fechar", gtk.RESPONSE_CLOSE)

	content, err := dlg.GetContentArea()
	if err != nil {
		return err
	}
	content.SetSpacing(6)

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return err
	}
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	list, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 4)
	if err != nil {
		return err
	}

	for _, row := range resultRows(rep) {
		rowBox, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 2)
		if err != nil {
			return err
		}

		hdr, err := gtk.LabelNew("")
		if err != nil {
			return err
		}
		hdr.SetXAlign(0)
		hdr.SetMarkup(fmt.Sprintf("<b>%s</b> — %s", gtkEscape(row.Target), gtkEscape(row.Label)))
		rowBox.PackStart(hdr, false, false, 0)

		if row.Detail != "" {
			detail, err := gtk.LabelNew(row.Detail)
			if err != nil {
				return err
			}
			detail.SetXAlign(0)
			detail.SetLineWrap(true)
			rowBox.PackStart(detail, false, false, 0)
		}

		if row.Err != "" {
			errLbl, err := gtk.LabelNew("")
			if err != nil {
				return err
			}
			errLbl.SetXAlign(0)
			errLbl.SetLineWrap(true)
			errLbl.SetMarkup(fmt.Sprintf(`<tt>%s</tt>`, gtkEscape(row.Err)))
			rowBox.PackStart(errLbl, false, false, 0)
		}

		list.PackStart(rowBox, false, false, 0)

		sep, err := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
		if err != nil {
			return err
		}
		list.PackStart(sep, false, false, 0)
	}

	note, err := gtk.LabelNew("")
	if err != nil {
		return err
	}
	note.SetXAlign(0)
	note.SetLineWrap(true)
	note.SetMarkup("Nada é desfeito automaticamente: os targets já aplicados/removidos " +
		"continuam como estão, mesmo quando outro falha.")
	list.PackStart(note, false, false, 0)

	scroller.Add(list)
	content.PackStart(scroller, true, true, 0)

	dlg.ShowAll()
	dlg.Run()
	return nil
}

// showNoticesDialog displays the warnings raised during an operation
// (unreachable password, Docker pointing at the loopback address, a target
// about to prompt for sudo), one per Notice in rep.Notices. Each line is
// built by noticeText from the Notice's Kind — never from text
// internal/app produced, since that package deliberately emits values, not
// prose, so the CLI can stay English while the GUI speaks Portuguese.
func showNoticesDialog(win *gtk.Window, rep *app.Report) error {
	dlg, err := gtk.DialogNew()
	if err != nil {
		return err
	}
	defer dlg.Destroy()
	dlg.SetTransientFor(win)
	dlg.SetTitle("Avisos")
	dlg.SetDefaultSize(480, 320)
	dlg.AddButton("Fechar", gtk.RESPONSE_CLOSE)

	content, err := dlg.GetContentArea()
	if err != nil {
		return err
	}
	content.SetSpacing(6)

	list, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 8)
	if err != nil {
		return err
	}

	for _, text := range noticeTexts(rep) {
		lbl, err := gtk.LabelNew(text)
		if err != nil {
			return err
		}
		lbl.SetXAlign(0)
		lbl.SetLineWrap(true)
		list.PackStart(lbl, false, false, 0)
	}

	content.PackStart(list, true, true, 0)

	dlg.ShowAll()
	dlg.Run()
	return nil
}
