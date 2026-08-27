// This file has no "gui" build tag on purpose: the Report -> display
// mapping it contains must not pull in GTK/cgo, so the four-outcome and
// per-notice mapping can run (and be tested) as part of the normal, non-GUI
// build too. See rows.go for the same pattern applied to proxy.Status.
package gui

import (
	"fmt"
	"strings"

	"proxy-helper/internal/app"
)

// resultRow is the display form of one target's outcome in an app.Report:
// what the result dialog shows, independent of any widget.
type resultRow struct {
	Target string
	// Label is the Portuguese, user-facing word for the outcome: "aplicado",
	// "removido", "pulado" or "falhou".
	Label string
	// Detail carries Result.Detail through unchanged — technical,
	// language-neutral context such as the file that was written or the
	// command that is missing.
	Detail string
	// Err is Result.Err's text, verbatim. It stays in English by project
	// convention: it comes from apt, git, systemctl, not from this
	// package. Empty when the target did not fail.
	Err string
}

// resultRowFor maps one Result into its display form. The four Outcome
// values are the only ones app.Result defines; an unrecognized value (which
// should never happen) falls back to an empty Label rather than panicking.
func resultRowFor(res app.Result) resultRow {
	row := resultRow{Target: res.Target, Detail: res.Detail}
	switch res.Outcome {
	case app.OutcomeApplied:
		row.Label = "aplicado"
	case app.OutcomeCleared:
		row.Label = "removido"
	case app.OutcomeSkipped:
		row.Label = "pulado"
	case app.OutcomeFailed:
		row.Label = "falhou"
	}
	if res.Err != nil {
		row.Err = res.Err.Error()
	}
	return row
}

// resultRows maps every Result in a Report into its display form, in the
// order app produced them.
func resultRows(rep *app.Report) []resultRow {
	rows := make([]resultRow, 0, len(rep.Results))
	for _, res := range rep.Results {
		rows = append(rows, resultRowFor(res))
	}
	return rows
}

// appliedCount returns how many of rep.Results actually applied or cleared
// — total minus the ones that were skipped (target unavailable) or failed.
// len(rep.Results) alone over-reports: a skipped or failed target still
// gets a Result, so counting every Result as a success claims things
// happened that did not.
func appliedCount(rep *app.Report) int {
	n := len(rep.Results)
	for _, res := range rep.Results {
		if res.Outcome == app.OutcomeSkipped || res.Outcome == app.OutcomeFailed {
			n--
		}
	}
	return n
}

// noticeText renders one Notice into Portuguese prose, built entirely from
// its Kind (and the language-neutral Target/Args it carries) — never from
// text app produces, since internal/app deliberately emits values instead
// of prose (see app's TestPackageDoesNotFormatUserFacingText). Wording
// mirrors cmd/render.go's English messages, adapted to Portuguese.
func noticeText(n app.Notice) string {
	switch n.Kind {
	case app.NoticeUnreachablePassword:
		return fmt.Sprintf(
			"a senha deste perfil vem de %s, que só o proxy local consegue ler; "+
				"os alvos configurados diretamente ficam com um usuário sem senha e vão "+
				"falhar ao autenticar. Use --via-local (veja \"proxy serve\") para manter a "+
				"credencial em um só lugar.",
			n.Args["source"])
	case app.NoticeDockerLoopback:
		return fmt.Sprintf(
			"%s vai apontar para 127.0.0.1, que containers não conseguem alcançar; "+
				"pulls funcionam, mas passos de build que precisem de rede vão falhar. "+
				"Rode \"proxy serve install --docker-bridge\" para o daemon também escutar "+
				"onde containers alcançam.",
			n.Target)
	case app.NoticeNeedsSudo:
		return fmt.Sprintf("%s precisa de sudo; você pode ser solicitado a digitar sua senha.", n.Target)
	case app.NoticeProfileAlreadyPlumbed:
		return "Perfil trocado; os alvos já apontam para o proxy local, nada mudou neles."
	case app.NoticeDockerNeedsRestart:
		if n.Args["op"] == "clear" {
			return "O Docker precisa ser reiniciado para a mudança valer."
		}
		return "O Docker precisa ser reiniciado para a mudança valer. " +
			"Os containers em execução serão reiniciados."
	default:
		return fmt.Sprintf("aviso desconhecido para %s", n.Target)
	}
}

// noticeTexts maps every Notice in a Report into Portuguese text, in the
// order app raised them.
func noticeTexts(rep *app.Report) []string {
	texts := make([]string, 0, len(rep.Notices))
	for _, n := range rep.Notices {
		texts = append(texts, noticeText(n))
	}
	return texts
}

// collapseRepeatedLines collapses runs of two or more consecutive,
// identical, non-blank lines into a single line annotated with the repeat
// count. It exists for buildPrivilegedOutputExpander (dialogs.go): the
// elevated CLI's raw stdout/stderr is shown verbatim, and a command that
// prints the same warning once per sub-invocation (as gsettings did for
// the dconf failure this fixes elsewhere) can otherwise drown the one line
// that actually matters under N identical copies of another. Blank-line
// runs are left alone — collapsing spacing has no benefit and would only
// make the output look broken.
func collapseRepeatedLines(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		count := j - i
		if count > 1 && strings.TrimSpace(lines[i]) != "" {
			out = append(out, fmt.Sprintf("%s (repetido %dx)", lines[i], count))
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n")
}
