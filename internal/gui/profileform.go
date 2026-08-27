// profileform.go has no "gui" build tag and imports no GTK/glib, on purpose:
// that is what makes it testable without a display, the same reasoning as
// jobs.go. It holds the Perfis page's form validation — the whole of its
// judgement about what the user typed — kept out of the GTK file so
// `go test ./...` exercises it without needing a graphical session.
package gui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"proxy-helper/internal/proxy"
)

// profileFormValues is what the form's entries hold, as plain strings —
// exactly as typed, before any interpretation.
type profileFormValues struct {
	Name, Scheme, Host, Port, User, Pass, NoProxy string
}

// validateProfileForm turns what the user typed into a Config, or returns
// the Portuguese message explaining what is wrong. It is the whole of the
// form's judgement, kept out of the GTK file so it can be tested without a
// display.
//
// existing is the profiles already on disk (pf.Profiles), used to reject a
// duplicate name; editing is the name of the profile being edited, if any —
// the name that is allowed to collide with itself. Every rule here mirrors
// what the CLI already refuses in cmd/proxy_profile.go; none is new.
func validateProfileForm(v profileFormValues, existing map[string]proxy.Config, editing string) (name string, cfg proxy.Config, errMsg string) {
	name, errMsg = validateProfileName(v.Name, existing, editing)
	if errMsg != "" {
		return "", proxy.Config{}, errMsg
	}

	if v.Host == "" {
		return "", proxy.Config{}, "O host do proxy é obrigatório."
	}

	scheme := v.Scheme
	if scheme == "" {
		scheme = "http"
	}
	switch scheme {
	case "http", "https", "socks5":
	default:
		return "", proxy.Config{}, fmt.Sprintf("Esquema inválido: %q. Use http, https ou socks5.", scheme)
	}

	if v.Port != "" {
		port, err := strconv.Atoi(v.Port)
		if err != nil || port < 1 || port > 65535 {
			return "", proxy.Config{}, fmt.Sprintf("Porta inválida: %q.", v.Port)
		}
	}

	noProxy := splitNoProxy(v.NoProxy)

	cfg = proxy.Config{
		Scheme:   scheme,
		Host:     v.Host,
		Port:     v.Port,
		Username: v.User,
		Password: v.Pass,
		NoProxy:  noProxy,
	}
	return name, cfg, ""
}

// validateProfileName is the name-checking core of validateProfileForm,
// factored out so importview.go's validateImportForm can reuse the same
// three rules (blank, reserved, duplicate) without duplicating them. editing
// is the name allowed to collide with itself (empty when there is no such
// exception, as in the import form, which never edits an existing profile).
func validateProfileName(name string, existing map[string]proxy.Config, editing string) (string, string) {
	if name == "" {
		return "", "O nome do perfil é obrigatório."
	}
	if name == proxy.CurrentProfileName {
		return "", fmt.Sprintf("%q é reservado para \"proxy set --via-local\". Escolha outro nome.", proxy.CurrentProfileName)
	}
	if _, exists := existing[name]; exists && name != editing {
		return "", fmt.Sprintf("Já existe um perfil chamado %q.", name)
	}
	return name, ""
}

// splitNoProxy turns a comma-separated list as typed into the slice the
// core expects: trimmed, with empty entries dropped. Shared by the
// per-profile No-proxy field (via validateProfileForm) and the global
// No-proxy panel, so both interpret what the user typed the same way.
//
// An empty or blank string yields a nil slice — not []string{""} (one empty
// entry, which would push "" down to every target) and not []string{} (a
// non-nil empty slice) either. That second distinction is load-bearing for
// the global list: ProfileFile.EffectiveGlobalNoProxy (internal/proxy/
// profiles.go) tests GlobalNoProxy != nil, not len == 0, so nil means "fall
// back to DefaultGlobalNoProxy" while a non-nil empty slice means "no host
// bypasses the proxy" — the opposite. Keep the loop building noProxy via
// append on a nil-initialized var (never `noProxy := []string{}`), or an
// emptied global no-proxy box silently inverts on save.
func splitNoProxy(s string) []string {
	var noProxy []string
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		noProxy = append(noProxy, item)
	}
	return noProxy
}

// formMode is whether the form is creating a profile or editing one.
type formMode int

const (
	modeNew formMode = iota
	modeEdit
)

// formTitle is the heading above the form. It exists because the only cue
// today is the Name field greying out, which is easy to miss.
//
// The edit-mode text quotes the profile name with typographic quotes
// (“ ” — U+201C/U+201D), not straight ones, so the name reads as a name
// dropped into the sentence rather than as a stray literal quote character.
func formTitle(mode formMode, name string) string {
	if mode == modeEdit {
		return fmt.Sprintf("Editando “%s”", name)
	}
	return "Novo perfil"
}

// primaryActionLabel is what the form's confirming button says. It names the
// operation instead of a generic "Salvar", so the button itself tells the
// user which mode they are in — the title is not the only cue, which
// matters because the title used to read as just another section heading.
func primaryActionLabel(mode formMode) string {
	if mode == modeEdit {
		return "Salvar alterações"
	}
	return "Criar perfil"
}

// isDirty says whether the form differs from the values it was filled with.
// Switching profiles discards edits, so this is what decides whether that
// needs a confirmation.
//
// profileFormValues holds only comparable string fields today, so a plain
// struct comparison covers all of them at once. If a []string (or other
// non-comparable) field is ever added to profileFormValues, this comparison
// stops compiling rather than silently ignoring the new field — do not
// "fix" that by comparing field-by-field unless the compiler forces it.
func isDirty(baseline, current profileFormValues) bool {
	return baseline != current
}

// canSave says whether Salvar has anything to do: a form that still matches
// what it was filled with has nothing to write, whether it is a new profile or
// an edit.
//
// It takes no formMode on purpose. An earlier version special-cased modeNew by
// comparing against the zero profileFormValues, which assumed "a blank new
// form" and "the zero struct" were the same thing. They are not: startNew
// selects a default scheme, so a untouched new form carries Scheme:"http" and
// that comparison reported it as savable — and, via isDirty, as unsaved work
// worth warning about. Comparing against the baseline the form was actually
// populated with is what makes both questions correct.
func canSave(baseline, current profileFormValues) bool {
	return isDirty(baseline, current)
}

// canCancel says whether there is anything for the Cancelar button to undo:
// Cancelar reverts the form to the values it was populated with, so with
// nothing changed it has no work and stays insensitive.
//
// This is the same comparison as isDirty today — canSave asks "is there
// something to write?" and canCancel asks "is there something to revert?",
// which happen to have the same answer right now. It still gets its own
// name at the call site because those are different questions that could
// diverge (e.g. a future autosave that keeps canSave false while a pending
// revert stays available), and because "no formMode parameter" already bit
// this file once: canSave used to take one, built on the assumption that a
// blank new form was interchangeable with the zero profileFormValues, and
// that premise had to be removed rather than patched. Keeping canCancel as
// its own function means a similar correction here, if one is ever needed,
// changes one function body instead of every call site.
func canCancel(baseline, current profileFormValues) bool {
	return isDirty(baseline, current)
}

// visibleProfiles returns the profiles a user should see, sorted, with the
// reserved "_current" slot left out — it is an implementation detail of
// "proxy set --via-local", not something the user saved, so showing it
// would invite editing it.
func visibleProfiles(pf *proxy.ProfileFile) []string {
	names := make([]string, 0, len(pf.Profiles))
	for name := range pf.Profiles {
		if name == proxy.CurrentProfileName {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// switchRoute says how activating a profile from the headerbar selector
// will behave, so the caller can warn the user before something expensive
// and privileged happens. This is the security-relevant decision the
// selector makes on every "changed" event — kept in this no-build-tag file,
// not headerbar.go, specifically so `go test ./...` can pin it down without
// a display: a mistake here (choosing routeRewritesTargets when it should
// be routeStateOnly, or vice versa) is not a cosmetic bug, it is either an
// unannounced pkexec password prompt or targets silently left unrewritten.
type switchRoute int

const (
	// routeStateOnly: the targets already point at the local daemon
	// (pf.ViaLocal), so switching profiles is pure state — instant, no
	// password, no target touched. Getting this wrong by returning
	// routeRewritesTargets here would rewrite plumbing that must stay
	// alone, tearing down the daemon setup and putting the upstream
	// credential back into every tool's own config file.
	routeStateOnly switchRoute = iota
	// routeRewritesTargets: the targets hold the upstream themselves, so
	// switching rewrites all of them and needs pkexec. Getting this wrong
	// by returning routeStateOnly here would skip the confirmation dialog
	// and the actual rewrite both — the profile would appear active while
	// every target still points at the old upstream.
	routeRewritesTargets
)

// routeForSwitch decides the route from pf.ViaLocal alone — see
// app.Enable's own doc comment (internal/app/profile.go) for why viaLocal
// being explicitly requested (Route 1 there) never reaches this decision:
// the headerbar selector only ever calls Enable with viaLocal=false, so
// pf.ViaLocal is the only input that matters here.
func routeForSwitch(viaLocal bool) switchRoute {
	if viaLocal {
		return routeStateOnly
	}
	return routeRewritesTargets
}

// currentSlotLabel is how the reserved "_current" slot is named in the
// headerbar selector. The slot is not a saved profile — it is the ad-hoc
// config "proxy set --via-local" writes — so it never appears in the
// profiles list, but it CAN be the active one, and then the selector has to
// show something. Showing nothing (what happened before) left the selector
// blank next to a master switch reading "Ativo", which is a contradiction.
const currentSlotLabel = "Configuração avulsa"

// headerbarExtraEntry returns the entry the headerbar selector needs beyond
// the saved profiles, given whatever is active. There are exactly two such
// cases and they are mutually exclusive with a saved profile being active:
// nothing active at all, and the reserved slot being active. ok is false
// when a saved profile is active, because then it IS the selection and a
// second inert row alongside it would only be something confusing to pick.
func headerbarExtraEntry(active string) (id, label string, ok bool) {
	switch active {
	case "":
		return "", "Nenhum perfil", true
	case proxy.CurrentProfileName:
		return proxy.CurrentProfileName, currentSlotLabel, true
	default:
		return "", "", false
	}
}
