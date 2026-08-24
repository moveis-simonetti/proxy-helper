//go:build gui

package gui

import (
	"bytes"
	"fmt"
	"strings"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/gotk3/gotk3/gtk"
)

// statusDeps builds the app.Deps the Status page needs. Collect and Elevate
// only ever touch ResolveTargets; DaemonActive, ReloadDaemon and BridgeAddr
// exist here for Apply (via --via-local, not exercised by this page's own
// buttons yet, but app.Apply dereferences them unconditionally when
// viaLocal is true) and are the real internal/serve implementations, not
// stubs — a GUI has no test double to fall back to.
func statusDeps() app.Deps {
	return app.Deps{
		ResolveTargets: proxy.ByNames,
		DaemonActive:   serve.DaemonActive,
		ReloadDaemon:   serve.ReloadDaemon,
		BridgeAddr:     serve.DockerBridgeAddr,
	}
}

// statusPageRow is one target's row of widgets in the grid.
type statusPageRow struct {
	name string
	root bool
	// sessionScoped mirrors proxy.Target.SessionScoped(): true for targets
	// that talk to the invoking user's desktop session (gnome, kde) and so
	// must never be routed into the pkexec-elevated call, regardless of
	// root. See selectedNames.
	sessionScoped bool
	checkbox      *gtk.CheckButton
	lock          *gtk.Label
	nameLbl       *gtk.Label
	state         *gtk.Label
	detail        *gtk.Label
}

// statusPage owns the widgets of the Status page: one grid row per target,
// in AllTargets() order, plus a footer with the "via daemon local" toggle
// and the selection summary.
type statusPage struct {
	runner *runner

	// topWindow is the shell window, used only as the transient-for parent
	// of the preview/result/notices dialogs (dialogs.go).
	topWindow *gtk.Window

	rows           []*statusPageRow
	plainReloadBtn *gtk.Button
	reloadBtn      *gtk.Button
	summary        *gtk.Label
	viaLocal       *gtk.CheckButton

	applyBtn    *gtk.Button
	clearBtn    *gtk.Button
	simulateBtn *gtk.Button
	resultLbl   *gtk.Label

	// lastStatuses is the most recent read, kept so "Recarregar com sudo"
	// can re-check exactly the targets that still need it instead of
	// re-reading everything.
	lastStatuses []proxy.Status

	// loaded marks that at least one successful read has already landed.
	// applyStatuses auto-selects every available target only on the first
	// one; see applyRow's doc comment for why later reloads must not repeat
	// that reset.
	loaded bool
}

// setupStatusPage fills win.StatusPage (built empty by newWindow) with the
// target table, and kicks off the first read through the runner so the
// window never blocks on gsettings/snap/file I/O on the UI thread.
func setupStatusPage(win *window, r *runner) (*statusPage, error) {
	sp := &statusPage{runner: r, topWindow: win.Window}

	scroller, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return nil, err
	}
	scroller.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)

	grid, err := gtk.GridNew()
	if err != nil {
		return nil, err
	}
	grid.SetRowSpacing(6)
	grid.SetColumnSpacing(12)

	headers := []string{"", "", "Target", "Estado", "Detalhe"}
	for col, text := range headers {
		lbl, err := gtk.LabelNew("")
		if err != nil {
			return nil, err
		}
		lbl.SetMarkup(fmt.Sprintf("<b>%s</b>", text))
		lbl.SetXAlign(0)
		grid.Attach(lbl, col, 0, 1, 1)
	}

	targets := proxy.AllTargets()
	sp.rows = make([]*statusPageRow, 0, len(targets))
	for i, t := range targets {
		row, err := newStatusPageRow(t.Name(), t.RequiresRoot(), t.SessionScoped())
		if err != nil {
			return nil, err
		}
		top := i + 1
		grid.Attach(row.checkbox, 0, top, 1, 1)
		grid.Attach(row.lock, 1, top, 1, 1)
		grid.Attach(row.nameLbl, 2, top, 1, 1)
		grid.Attach(row.state, 3, top, 1, 1)
		grid.Attach(row.detail, 4, top, 1, 1)

		row.checkbox.Connect("toggled", func() { sp.updateSummary() })

		sp.rows = append(sp.rows, row)
	}

	scroller.Add(grid)
	win.StatusPage.PackStart(scroller, true, true, 0)

	sep, err := gtk.SeparatorNew(gtk.ORIENTATION_HORIZONTAL)
	if err != nil {
		return nil, err
	}
	win.StatusPage.PackStart(sep, false, false, 0)

	footer, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	if err != nil {
		return nil, err
	}

	viaLocal, err := gtk.CheckButtonNew()
	if err != nil {
		return nil, err
	}
	viaLocalLbl, err := gtk.LabelNew("Via daemon local")
	if err != nil {
		return nil, err
	}
	footer.PackStart(viaLocal, false, false, 0)
	footer.PackStart(viaLocalLbl, false, false, 0)
	sp.viaLocal = viaLocal

	summary, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	footer.PackStart(summary, true, true, 0)
	sp.summary = summary

	// Plain reload: always visible, independent of whether any target needs
	// elevation. Without it, a Collect error (applyStatuses paints every row
	// "erro" and disables every checkbox and button) has no way back short
	// of restarting the whole app, since load() otherwise only ever runs
	// once, at setup.
	plainReloadBtn, err := gtk.ButtonNewWithLabel("Recarregar")
	if err != nil {
		return nil, err
	}
	plainReloadBtn.Connect("clicked", func() { sp.load() })
	footer.PackStart(plainReloadBtn, false, false, 0)
	sp.plainReloadBtn = plainReloadBtn

	reloadBtn, err := gtk.ButtonNewWithLabel("Recarregar com sudo")
	if err != nil {
		return nil, err
	}
	// The button only makes sense once a read reveals a target that needs
	// elevation, which happens asynchronously after the first load. Hidden
	// at construction time; setNoShowAll keeps a later ShowAll() on the
	// window from reasserting visibility before that first result lands.
	reloadBtn.SetNoShowAll(true)
	reloadBtn.SetVisible(false)
	reloadBtn.Connect("clicked", func() { sp.reloadWithSudo() })
	footer.PackStart(reloadBtn, false, false, 0)
	sp.reloadBtn = reloadBtn

	win.StatusPage.PackStart(footer, false, false, 0)

	actions, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 12)
	if err != nil {
		return nil, err
	}

	applyBtn, err := gtk.ButtonNewWithLabel("Aplicar")
	if err != nil {
		return nil, err
	}
	applyBtn.Connect("clicked", func() { sp.apply() })
	actions.PackStart(applyBtn, false, false, 0)
	sp.applyBtn = applyBtn

	clearBtn, err := gtk.ButtonNewWithLabel("Remover")
	if err != nil {
		return nil, err
	}
	clearBtn.Connect("clicked", func() { sp.clear() })
	actions.PackStart(clearBtn, false, false, 0)
	sp.clearBtn = clearBtn

	simulateBtn, err := gtk.ButtonNewWithLabel("Simular")
	if err != nil {
		return nil, err
	}
	simulateBtn.Connect("clicked", func() { sp.simulate() })
	actions.PackStart(simulateBtn, false, false, 0)
	sp.simulateBtn = simulateBtn

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	actions.PackStart(resultLbl, true, true, 0)
	sp.resultLbl = resultLbl

	win.StatusPage.PackStart(actions, false, false, 0)

	// Disable the buttons that submit a job while one is already running:
	// the runner is strictly one-job-at-a-time (see jobs.go), and a second
	// click landing mid-Apply would just queue up and run later with a
	// stale view of the checkboxes, not run concurrently — better to make
	// that unavailable than confusing.
	r.setBusyHandler(func(busy bool) {
		enabled := !busy
		sp.plainReloadBtn.SetSensitive(enabled)
		sp.reloadBtn.SetSensitive(enabled)
		sp.applyBtn.SetSensitive(enabled)
		sp.clearBtn.SetSensitive(enabled)
		sp.simulateBtn.SetSensitive(enabled)
	})

	sp.updateSummary()
	sp.load()

	return sp, nil
}

// newStatusPageRow builds one row's widgets, unpopulated: applyRow fills
// in the state/detail text once a read comes back.
func newStatusPageRow(name string, root, sessionScoped bool) (*statusPageRow, error) {
	checkbox, err := gtk.CheckButtonNew()
	if err != nil {
		return nil, err
	}

	lock, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	// A session-scoped target (gnome, kde) is never routed through the
	// privileged pkexec path (see SessionScoped's doc comment and
	// splitSessionAware in elevate.go) — it always runs in-process, so the
	// padlock would be lying about a prompt that can never happen.
	if root && !sessionScoped {
		lock.SetMarkup("\U0001F512")
		lock.SetTooltipText("requer privilégios de root para aplicar")
	}

	nameLbl, err := gtk.LabelNew(name)
	if err != nil {
		return nil, err
	}
	nameLbl.SetXAlign(0)

	state, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	state.SetXAlign(0)

	detail, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	detail.SetXAlign(0)

	return &statusPageRow{
		name:          name,
		root:          root,
		sessionScoped: sessionScoped,
		checkbox:      checkbox,
		lock:          lock,
		nameLbl:       nameLbl,
		state:         state,
		detail:        detail,
	}, nil
}

// applyRow renders one target's rowFor() result onto its widgets. Called
// on the UI thread only, from a runner delivery.
//
// resetSelection controls what happens to the checkbox's Active state: true
// (the very first load) auto-selects every available target, matching the
// original "everything on, uncheck what you don't want" default. false
// (every reload after that — post-Apply/Clear, or "Recarregar com sudo")
// leaves whatever the user last chose alone, only forcing the box off when
// the target just became unselectable. Without that distinction, a reload
// after Apply/Clear silently re-checks every available target regardless of
// what was actually selected — so a Clear right after an Apply of just one
// target would wipe out every other target's settings too, not only the
// one the user picked.
func (row *statusPageRow) applyRow(r statusRow, resetSelection bool) {
	row.checkbox.SetSensitive(r.Selectable)
	switch {
	case !r.Selectable:
		row.checkbox.SetActive(false)
	case resetSelection:
		row.checkbox.SetActive(true)
	}

	row.state.SetMarkup(fmt.Sprintf(`<span foreground="%s">%s</span>`, r.Color, gtkEscape(r.Label)))

	detailColor := ""
	if !r.Selectable {
		detailColor = r.Color
	}
	if detailColor != "" {
		row.detail.SetMarkup(fmt.Sprintf(`<span foreground="%s">%s</span>`, detailColor, gtkEscape(r.Detail)))
		row.nameLbl.SetMarkup(fmt.Sprintf(`<span foreground="%s">%s</span>`, detailColor, gtkEscape(r.Name)))
	} else {
		row.detail.SetText(r.Detail)
		row.nameLbl.SetText(r.Name)
	}
}

// gtkEscape escapes text that is about to be embedded in a Pango markup
// string built with fmt.Sprintf, so a target Detail containing "<" or "&"
// (seen in real error text, e.g. from apt or git) cannot break the markup
// or be misread as a tag.
func gtkEscape(s string) string {
	b := &bytes.Buffer{}
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// load runs app.Collect off the UI thread through the runner: it shells
// out to gsettings, snap get, reads files, and can take seconds — running
// it inline would freeze the window.
func (sp *statusPage) load() {
	sp.runner.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone, Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
		sts, err := app.Collect(statusDeps(), ex, []string{"all"}, false)
		return func() { sp.applyStatuses(sts, err) }
	})
}

// reloadWithSudo re-checks only the targets app.NeedsElevation flagged,
// via app.Elevate, off the UI thread.
//
// This is the one Executor in this file that must use EscalatePkexec, not
// EscalateNone: EscalateNone is for in-process work that must never prompt
// (the plain status reads and the user-level target writes elsewhere in this
// file), while EscalatePkexec is for a path whose entire purpose is to
// elevate. This button exists only to let targets read privileged state
// (e.g. snap) that a non-root read can't see; with EscalateNone that read
// fails ("requires root, but elevation is disabled"), and since elevate==true
// skips the access-denied recovery path, the error propagates all the way up
// and turns an orange "requer sudo" row into a grey "indisponível" one — the
// button making things strictly worse. Do not change this back to
// EscalateNone.
func (sp *statusPage) reloadWithSudo() {
	sp.runner.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalatePkexec, Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
		sts, err := app.Elevate(statusDeps(), ex, sp.lastStatuses)
		return func() { sp.applyStatuses(sts, err) }
	})
}

// applyStatuses renders a Collect/Elevate result onto the grid. Runs on the
// UI thread: it is always called from a closure a runner job returned.
func (sp *statusPage) applyStatuses(sts []proxy.Status, err error) {
	if err != nil {
		for _, row := range sp.rows {
			row.state.SetMarkup(`<span foreground="#c64600">erro</span>`)
			row.detail.SetText(err.Error())
			row.checkbox.SetSensitive(false)
			row.checkbox.SetActive(false)
		}
		sp.reloadBtn.SetVisible(false)
		sp.updateSummary()
		return
	}

	sp.lastStatuses = sts

	byName := make(map[string]proxy.Status, len(sts))
	for _, st := range sts {
		byName[st.Name] = st
	}

	// Only the very first successful load auto-selects every available
	// target; every later reload (post-Apply/Clear, "Recarregar com
	// sudo") preserves whatever the user has checked. See applyRow's doc
	// comment for why that distinction matters.
	resetSelection := !sp.loaded
	sp.loaded = true

	for _, row := range sp.rows {
		st, ok := byName[row.name]
		if !ok {
			continue
		}
		row.applyRow(rowFor(st, row.root), resetSelection)
	}

	sp.reloadBtn.SetVisible(app.NeedsElevation(sts))
	sp.updateSummary()
}

// updateSummary refreshes the "N de M targets selecionados" footer text
// from the checkboxes' current state.
func (sp *statusPage) updateSummary() {
	selected := 0
	for _, row := range sp.rows {
		if row.checkbox.GetActive() {
			selected++
		}
	}
	sp.summary.SetText(fmt.Sprintf("%d de %d targets selecionados", selected, len(sp.rows)))
}

// selectedNames splits the checked targets into the user-level ones this
// page applies in-process and the privileged ones that go through a single
// pkexec call, via splitSessionAware (elevate.go) — see its doc comment for
// why a session-scoped target is never privileged here regardless of Root.
// Reads only the checkbox state, so it is safe to call from the UI thread
// before submitting a job.
func (sp *statusPage) selectedNames() (user, privileged []string) {
	var selected []selectedTargetInfo
	for _, row := range sp.rows {
		if !row.checkbox.GetActive() {
			continue
		}
		selected = append(selected, selectedTargetInfo{
			Name:          row.name,
			Root:          row.root,
			SessionScoped: row.sessionScoped,
		})
	}
	return splitSessionAware(selected)
}

// selectedTargets returns every checked target, user and privileged alike —
// what simulate() needs, since a dry run never actually touches a
// privileged file or command and so never needs the pkexec split.
func (sp *statusPage) selectedTargets() []string {
	var names []string
	for _, row := range sp.rows {
		if row.checkbox.GetActive() {
			names = append(names, row.name)
		}
	}
	return names
}

// activeProfile loads the profiles file and resolves the active profile's
// config, returning the ProfileFile too (a caller applying "Via daemon
// local" to privileged targets needs its EffectiveLocalPort/
// EffectiveGlobalNoProxy/DockerBridge, not just the resolved Config). It
// does file I/O, so every caller runs it inside a runner job, off the UI
// thread. There is no profile picker in the GUI yet (that is a later
// phase's page); until then, Apply/Simular act on whatever
// "proxy profile use" or the CLI last left active.
func activeProfile() (string, proxy.Config, *proxy.ProfileFile, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return "", proxy.Config{}, nil, err
	}
	if pf.ActiveProfile == "" {
		return "", proxy.Config{}, nil, fmt.Errorf("nenhum perfil ativo (veja \"proxy profile use\")")
	}
	cfg, ok := pf.Get(pf.ActiveProfile)
	if !ok {
		return "", proxy.Config{}, nil, fmt.Errorf("perfil %q não encontrado", pf.ActiveProfile)
	}
	return pf.ActiveProfile, cfg, pf, nil
}

// showResult renders msg into the footer's result label. Runs on the UI
// thread only, from a runner delivery.
func (sp *statusPage) showResult(msg string) {
	sp.resultLbl.SetText(msg)
}

// applyPrivileged runs the single pkexec call covering every privileged
// target selected, and turns its outcome into a one-line summary shown in
// the footer (there is no per-target Result for these: they run through a
// reinvoked CLI, not app.Apply, so they cannot feed the result dialog).
// Called off the UI thread, from inside a runner job.
//
// The summary deliberately does not claim a count of "N aplicados": the CLI
// may have skipped a target as unavailable, and len(targets) only ever
// reflects what was asked for, not what actually happened — asserting a
// number here would misreport a partial skip as a full success, and a
// single target's real failure as if every target had failed. The CLI's
// own per-target table (out) is captured by runCmd; the caller shows it in
// the result dialog so the user reads the real per-target outcome instead.
func (sp *statusPage) applyPrivileged(profile string, targets []string) (summary, cliOutput string) {
	binary, err := findCLIBinary()
	if err != nil {
		return fmt.Sprintf("privilegiados não aplicados: %s", err), ""
	}
	xdgConfigHome, err := resolveConfigHome()
	if err != nil {
		return fmt.Sprintf("privilegiados não aplicados: %s", err), ""
	}
	out, cancelled, err := runCmd(elevateCmd(binary, profile, targets, xdgConfigHome))
	switch {
	case cancelled:
		return "privilegiados: autorização cancelada, nada mudou", ""
	case err != nil:
		return fmt.Sprintf("privilegiados falharam: %s (%s)", err, strings.TrimSpace(out)), out
	default:
		return "privilegiados: processados, veja o resultado", out
	}
}

// applyPrivilegedViaLocal is applyPrivileged's counterpart for "Via daemon
// local": instead of --profile, it resolves the loopback (and, when the
// Docker bridge is on and dockerd is selected, the bridge) address itself
// and calls elevateViaLocalCmds — one pkexec call, or two when dockerd needs
// the bridge and other privileged targets still need loopback (see that
// function's doc comment). Called off the UI thread, from inside a runner
// job; the caller (apply()) is responsible for warning about a second
// polkit dialog *before* this runs, via needsTwoElevatedCalls.
//
// noProxy must already be the merged (global + profile) list app.Apply
// would have produced for the user-level targets — see MergeNoProxy. The
// elevated CLI still merges it again, against root's own global list; since
// XDG_CONFIG_HOME is passed through, that is normally the very same list
// this function just merged, so the second merge is a harmless no-op. It
// only diverges if root cannot resolve/read that config (e.g. XDG_CONFIG_HOME
// pointing somewhere unreadable), in which case root falls back to
// proxy.DefaultGlobalNoProxy — so a default the user deliberately removed
// from their own global list (localhost, 127.0.0.1, host.docker.internal)
// could come back, but only for the privileged targets. There is no CLI flag
// to suppress that fallback merge; do not add one to work around it.
func (sp *statusPage) applyPrivilegedViaLocal(port int, noProxy, targets []string, dockerBridge bool) (summary, cliOutput string) {
	binary, err := findCLIBinary()
	if err != nil {
		return fmt.Sprintf("privilegiados não aplicados: %s", err), ""
	}
	xdgConfigHome, err := resolveConfigHome()
	if err != nil {
		return fmt.Sprintf("privilegiados não aplicados: %s", err), ""
	}

	bridgeAddr := ""
	if dockerBridge {
		addr, err := serve.DockerBridgeAddr()
		if err != nil {
			return fmt.Sprintf("privilegiados não aplicados: bridge do Docker indisponível: %s", err), ""
		}
		bridgeAddr = addr
	}

	cmds := elevateViaLocalCmds(binary, port, noProxy, targets, dockerBridge, bridgeAddr, xdgConfigHome)

	var outs []string
	for _, cmd := range cmds {
		out, cancelled, err := runCmd(cmd)
		if out != "" {
			outs = append(outs, out)
		}
		switch {
		case cancelled:
			return "privilegiados: autorização cancelada, nada mudou", strings.Join(outs, "\n")
		case err != nil:
			return fmt.Sprintf("privilegiados falharam: %s (%s)", err, strings.TrimSpace(out)), strings.Join(outs, "\n")
		}
	}
	return "privilegiados: processados, veja o resultado", strings.Join(outs, "\n")
}

// clearPrivileged is applyPrivileged's counterpart for Remover: same single
// pkexec call, but reinvoking "proxy unset" instead of "proxy set", since
// clearing never needs a profile. See applyPrivileged's doc comment for why
// the summary never claims a count of "N removidos".
func (sp *statusPage) clearPrivileged(targets []string) (summary, cliOutput string) {
	binary, err := findCLIBinary()
	if err != nil {
		return fmt.Sprintf("privilegiados não removidos: %s", err), ""
	}
	out, cancelled, err := runCmd(elevateUnsetCmd(binary, targets))
	switch {
	case cancelled:
		return "privilegiados: autorização cancelada, nada mudou", ""
	case err != nil:
		return fmt.Sprintf("privilegiados falharam: %s (%s)", err, strings.TrimSpace(out)), out
	default:
		return "privilegiados: processados, veja o resultado", out
	}
}

// apply applies the current selection: the user-level targets in-process,
// with escalation refused so an unexpected root requirement fails visibly
// instead of hanging on a prompt nobody can see, and the privileged targets
// through the single pkexec call in applyPrivileged. Runs off the UI
// thread via the runner; reloads the table afterwards so the grid reflects
// what actually happened rather than what was merely attempted.
func (sp *statusPage) apply() {
	user, privileged := sp.selectedNames()
	// Read on the UI thread, right next to the selection: the job body
	// below runs on the runner's worker goroutine, and GetActive on a
	// widget from there is undefined behaviour (see this method's doc
	// comment and reloadWithSudo's neighbours for the same pattern applied
	// to the checkboxes).
	viaLocal := sp.viaLocal.GetActive()
	if len(user) == 0 && len(privileged) == 0 {
		sp.showResult("nenhum target selecionado")
		return
	}

	// "Via daemon local" plus privileged targets used to be refused outright
	// here: pkexec runs the reinvoked CLI as root with no XDG_RUNTIME_DIR
	// and no user systemd manager, so its DaemonActive() check always
	// reported the daemon as stopped even when it was running, and
	// app.Apply failed every privileged target with a message that was
	// simply false (see elevateCmd's doc comment). The fix is to never pass
	// --via-local into the elevated CLI at all: applyPrivilegedViaLocal
	// resolves the loopback/bridge address itself and passes it as an
	// explicit --host, which needs neither DaemonActive() nor a profile
	// lookup. That address carries no credentials (proxy.TargetConfig
	// strips them for every --via-local target), so passing it as a
	// command-line flag does not leak a password to `ps` the way --pass
	// would.
	//
	// One combination still needs a heads-up before anything runs: Docker
	// bridge enabled and dockerd among the selected privileged targets
	// means two separate pkexec calls (dockerd needs the bridge address,
	// the rest need loopback — see elevateViaLocalCmds), i.e. two polkit
	// password prompts. Warn about that now, synchronously, before
	// submitting the job: LoadProfiles here is a quick local read used only
	// to decide whether to warn, not the authoritative one — the job below
	// reloads it itself. A failure here just means no warning, not a
	// blocked apply.
	if viaLocal && len(privileged) > 0 {
		if pf, err := proxy.LoadProfiles(); err == nil && needsTwoElevatedCalls(privileged, pf.DockerBridge) {
			sp.showResult(twoDialogsWarning)
		}
	}

	sp.runner.submit(func() func() {
		profile, cfg, pf, err := activeProfile()
		if err != nil {
			return func() { sp.showResult(err.Error()) }
		}

		var rep *app.Report
		var userErr error
		if len(user) > 0 {
			ex := &proxy.Executor{Escalation: proxy.EscalateNone, Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
			rep, userErr = app.Apply(statusDeps(), ex, cfg, user, viaLocal)
		}

		var privMsg, privOut string
		if len(privileged) > 0 {
			if viaLocal {
				port := pf.EffectiveLocalPort()
				mergedNoProxy := proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)
				privMsg, privOut = sp.applyPrivilegedViaLocal(port, mergedNoProxy, privileged, pf.DockerBridge)
			} else {
				privMsg, privOut = sp.applyPrivileged(profile, privileged)
			}
		}

		return func() {
			sp.showResult(joinNonEmpty(userSummary(rep, "aplicados"), privMsg))
			sp.load()
			sp.showApplyClearResult(rep, userErr, privOut)
		}
	})
}

// twoDialogsWarning tells the user up front that this apply needs two
// pkexec calls, not one, so a second unannounced password prompt does not
// look like the first one failing. It fires only for the one combination
// that needs it: Docker bridge enabled and dockerd among the selected
// privileged targets — dockerd needs the bridge address, the rest need
// loopback, and a single --host cannot serve both (see
// elevateViaLocalCmds's doc comment). Replaced by the real result once both
// calls finish.
const twoDialogsWarning = "\"Via daemon local\": bridge do Docker habilitada e dockerd selecionado vão pedir a senha " +
	"duas vezes — uma para o dockerd (endereço da bridge) e outra para os demais targets privilegiados (loopback)."

// clear removes the current selection: same in-process/pkexec split as
// apply, but through app.Clear and "proxy unset" — neither needs a profile.
func (sp *statusPage) clear() {
	user, privileged := sp.selectedNames()
	if len(user) == 0 && len(privileged) == 0 {
		sp.showResult("nenhum target selecionado")
		return
	}

	sp.runner.submit(func() func() {
		var rep *app.Report
		var userErr error
		if len(user) > 0 {
			ex := &proxy.Executor{Escalation: proxy.EscalateNone, Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
			rep, userErr = app.Clear(statusDeps(), ex, user)
		}

		var privMsg, privOut string
		if len(privileged) > 0 {
			privMsg, privOut = sp.clearPrivileged(privileged)
		}

		return func() {
			sp.showResult(joinNonEmpty(userSummary(rep, "removidos"), privMsg))
			sp.load()
			sp.showApplyClearResult(rep, userErr, privOut)
		}
	})
}

// userSummary renders a one-line count of what app.Apply/app.Clear actually
// did for the user-level targets: rep is nil when none were selected. See
// appliedCount for why the count excludes skipped/failed results.
func userSummary(rep *app.Report, verb string) string {
	if rep == nil {
		return ""
	}
	return fmt.Sprintf("usuário: %d %s", appliedCount(rep), verb)
}

// joinNonEmpty joins the non-empty parts with "; ", skipping any part that
// is empty (e.g. userSummary when no user-level target was selected).
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}

// showApplyClearResult opens the result dialog for the user-level part of
// an apply/clear (rep is nil when no user-level target was selected), the
// notices dialog afterwards if the operation raised any, and — since there
// is no per-target Result for the privileged half, only the elevated CLI's
// captured output — a dialog showing that output when privOut is non-empty.
// Runs on the UI thread only, from a runner delivery. userErr covers
// app.Apply/app.Clear itself failing outright (e.g. no active profile)
// rather than a single target failing, which is instead one row in rep with
// OutcomeFailed.
func (sp *statusPage) showApplyClearResult(rep *app.Report, userErr error, privOut string) {
	if userErr != nil {
		sp.showResult(fmt.Sprintf("erro: %s", userErr))
		return
	}
	if rep != nil {
		if err := showResultDialog(sp.topWindow, rep); err != nil {
			sp.showResult(fmt.Sprintf("erro ao abrir resultado: %s", err))
			return
		}
		if len(rep.Notices) > 0 {
			if err := showNoticesDialog(sp.topWindow, rep); err != nil {
				sp.showResult(fmt.Sprintf("erro ao abrir avisos: %s", err))
			}
		}
	}
	if privOut != "" {
		if err := showPrivilegedOutputDialog(sp.topWindow, privOut); err != nil {
			sp.showResult(fmt.Sprintf("erro ao abrir resultado privilegiado: %s", err))
		}
	}
}

// simulate previews the current selection with app.Apply in dry-run, over
// every checked target at once — a dry run never writes anything, so there
// is no in-process/pkexec split to make. The Executor is left at its zero
// Escalation value (EscalateSudo): DryRun means RunPrivileged only ever
// prints "[dry-run] would run (sudo): ..." and never actually shells out to
// sudo, so this cannot prompt or block. A real results dialog is a later
// task's job; this shows the preview text as-is in the result label.
func (sp *statusPage) simulate() {
	names := sp.selectedTargets()
	// Read on the UI thread, right next to the selection: see apply()'s
	// equivalent comment — the job body below runs off the UI thread.
	viaLocal := sp.viaLocal.GetActive()
	if len(names) == 0 {
		sp.showResult("nenhum target selecionado")
		return
	}

	sp.runner.submit(func() func() {
		_, cfg, _, err := activeProfile()
		if err != nil {
			return func() { sp.showResult(err.Error()) }
		}

		buf := &bytes.Buffer{}
		ex := &proxy.Executor{DryRun: true, Out: buf}
		_, err = app.Apply(statusDeps(), ex, cfg, names, viaLocal)

		if err != nil {
			return func() { sp.showResult(fmt.Sprintf("erro: %s", err)) }
		}
		text := buf.String()
		return func() {
			if err := showPreviewDialog(sp.topWindow, text); err != nil {
				sp.showResult(fmt.Sprintf("erro ao abrir prévia: %s", err))
			}
		}
	})
}
