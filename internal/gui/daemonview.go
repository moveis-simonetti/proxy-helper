// daemonview.go has no "gui" build tag and imports no GTK/glib, on purpose:
// the same reasoning as jobs.go and profileform.go. It holds the Daemon
// page's pure formatting logic — turning serve.LogEntry and service state
// into display strings — kept out of the GTK file so `go test ./...`
// exercises it without needing a graphical session.
package gui

import (
	"fmt"
	"strings"

	"proxy-helper/internal/serve"
)

// logRow is one journal entry formatted for the table. Every field is the
// string the cell shows; SortTime and SortDuration are the hidden numeric
// keys the columns sort by, because sorting "12 ms" and "9 ms" as text puts
// them in the wrong order.
type logRow struct {
	Time         string // HH:MM:SS
	Method       string
	Target       string // host, or host:port when the port is not the scheme default
	Route        string // "proxy" | "direto"
	Status       string // "200", or "erro" when the entry carries one
	Duration     string // "12 ms"
	SortTime     int64  // unix nanos
	SortDuration int64  // milliseconds
}

// logRows formats journal entries for the table. Callers are expected to
// have already run entries through serve.Match (or an equivalent filter):
// serve.Match drops anything whose Msg isn't "request", since lifecycle
// entries (startup, reload) carry none of the request columns this table
// shows. logRows does not refilter, but it also does not assume the
// filtering happened — an unfiltered lifecycle entry is a zero-value
// LogEntry as far as these fields go, so it renders as a (mostly empty,
// harmless) row instead of panicking.
func logRows(entries []serve.LogEntry) []logRow {
	rows := make([]logRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, logRow{
			Time:         e.Time.Format("15:04:05"),
			Method:       e.Method,
			Target:       formatTarget(e.Host, e.Port),
			Route:        formatRoute(e.Decision),
			Status:       formatStatus(e.Status, e.Err),
			Duration:     fmt.Sprintf("%d ms", e.Duration),
			SortTime:     e.Time.UnixNano(),
			SortDuration: e.Duration,
		})
	}
	return rows
}

// formatTarget renders the host, adding ":port" when a port was recorded.
func formatTarget(host, port string) string {
	if port == "" {
		return host
	}
	return host + ":" + port
}

// formatRoute renders the routing decision.
func formatRoute(decision string) string {
	if decision == "direct" {
		return "direto"
	}
	return "proxy"
}

// formatStatus renders the response status, preferring the error text when
// the entry carries one.
func formatStatus(status int, errText string) string {
	if errText != "" {
		return "erro"
	}
	if status == 0 {
		return "—"
	}
	return fmt.Sprintf("%d", status)
}

// daemonSummary is the service block's read-only text.
type daemonSummary struct {
	State   string // "Ativo" | "Parado" | "Não instalado"
	Listen  string // observed addresses, or "—" when bound to none
	Profile string // active profile, or "nenhum"
}

// summarize formats the service block's read-only text from the daemon's
// current state.
//
// listening is what serve.ListeningAddrs actually observed on the machine.
// The configured port is deliberately not an argument: deriving this line
// from config was the bug. A unit installed without --docker-bridge
// alongside a config saying docker_bridge:true made this field advertise a
// bridge address nothing was bound to, and a user hitting the opposite case
// had no way to see it from here.
func summarize(active, installed bool, listening []string, activeProfile string) daemonSummary {
	state := "Não instalado"
	switch {
	case active:
		state = "Ativo"
	case installed:
		state = "Parado"
	}

	// Nothing answering is a real answer, and the honest one for a stopped
	// or uninstalled service. State already says which of those it is.
	listen := "—"
	if len(listening) > 0 {
		listen = strings.Join(listening, ", ")
	}

	profile := activeProfile
	if profile == "" {
		profile = "nenhum"
	}

	return daemonSummary{
		State:   state,
		Listen:  listen,
		Profile: profile,
	}
}

// daemonFormValues is the service block's editable state: the desired port
// and Docker bridge preference. The bridge switch does not apply itself
// (see page_daemon.go's setup) — it only changes what this struct holds on
// screen, and daemonPending is what compares that against a baseline read
// from disk to decide whether the primary button has anything to send.
type daemonFormValues struct {
	Port         int
	DockerBridge bool
}

// daemonPending reports whether current differs from the baseline the
// service block was last loaded with. Comparable struct, so a plain `!=`
// covers both fields at once — same reasoning as profileform.go's isDirty.
func daemonPending(baseline, current daemonFormValues) bool {
	return baseline != current
}

// daemonPrimaryActionLabel is what the service block's primary button says.
// Named distinctly from profileform.go's primaryActionLabel: that one is the
// Perfis form's own button label and takes a formMode, an unrelated concept
// to this page's installed/not-installed state.
func daemonPrimaryActionLabel(installed bool) string {
	if installed {
		return "Aplicar alterações"
	}
	return "Instalar serviço"
}

// sameRows reports whether two consecutive reads produced identical tables.
//
// It exists for the live refresh: rebuilding the ListStore drops the user's
// selection and scroll position, and at one tick every couple of seconds a
// quiet proxy would do that forever for no reason. Comparing first means the
// table only churns when something actually arrived.
//
// logRow holds only comparable fields, so == covers all of them. If a slice
// or map field is ever added, this stops compiling instead of silently
// ignoring the new field — do not "fix" that by comparing field by field.
func sameRows(a, b []logRow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// portChangeStrandsTargets reports whether changing the daemon's port left
// the targets behind.
//
// applyPrimary persists the new port and reinstalls the unit, but it does
// not re-apply the targets — that is the Status page's job, over the user's
// own selection. So every target keeps the old port until the user applies
// again, and the failure shows up later, detached from the change that
// caused it. This is what the warning is for.
func portChangeStrandsTargets(oldPort, newPort int) bool {
	return oldPort != newPort
}

// staleTargetsWarning explains the situation portChangeStrandsTargets
// detects. It names both ports: what the targets still say, and what they
// should say — without which the user cannot tell whether a later failure
// is this or something else.
func staleTargetsWarning(oldPort, newPort int) string {
	return fmt.Sprintf(
		"os alvos ainda apontam para a porta %d, não para %d — reaplique na aba Status",
		oldPort, newPort)
}

// daemonApplyMessage builds the line applyPrimary shows after saving. The
// warning rides along with the confirmation rather than in a widget of its
// own: it belongs to the action the user just took, and a second label
// competing with this one is a label nobody reads.
func daemonApplyMessage(wasInstalled bool, warning string) string {
	msg := "serviço instalado"
	if wasInstalled {
		msg = "alterações aplicadas"
	}
	if warning != "" {
		msg += " — " + warning
	}
	return msg
}

// daemonPendingNotice is the line shown while the form holds changes that
// were never sent to the system.
//
// The bridge switch and the port field change intent only; the primary
// button applies them. That is the design, but the sole feedback used to be
// that button quietly turning sensitive — and a user who turned the bridge
// switch on, closed the window, and never pressed it reported the feature as
// broken. Saying it in words, next to the control, is the fix.
func daemonPendingNotice(pending bool) string {
	if !pending {
		return ""
	}
	return fmt.Sprintf("alterações não aplicadas — clique em %q", daemonPrimaryActionLabel(true))
}
