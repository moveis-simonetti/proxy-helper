//go:build gui

package gui

import (
	"bytes"
	"fmt"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/gotk3/gotk3/gtk"
)

// headerbarCtl owns the profile selector (win.ProfileCombo) and the master
// switch (win.MasterSwitch/win.StatusLabel), plus the reload cycle that
// keeps both honest with config.json.
type headerbarCtl struct {
	combo        *gtk.ComboBoxText
	masterSwitch *gtk.Switch
	statusLabel  *gtk.Label
	runner       *runner
	topWindow    *gtk.Window

	// onProfileChanged is called after a successful Enable, so the Status
	// page's own view of target state stays in sync with whatever the
	// selector just did. Wired from app.go, which is the only place that
	// has both the headerbarCtl and the statusPage in hand.
	onProfileChanged func()

	// repopulating is true for the duration of applyProfiles rebuilding the
	// combo's entries. GtkComboBoxText fires "changed" for RemoveAll,
	// Append and SetActiveID alike, not only for a real user selection —
	// without this guard, refresh() repopulating the combo would itself
	// trigger the "changed" handler, which would try to Enable the
	// newly-selected (but not actually user-chosen) profile, which calls
	// refresh() again on success: an activation loop. Every place that
	// mutates the combo's contents or active item programmatically must
	// set this first.
	repopulating bool

	// current is the profile name (or "" for none) the combo last actually
	// settled on — what revert() restores the combo to when the user
	// declines the route-3 confirmation dialog or an Enable call fails.
	// GTK has no "undo the last selection" primitive, so this is tracked by
	// hand.
	current string
}

// setupHeaderbar wires win.ProfileCombo (built empty by newWindow) to
// config.json: it populates the combo, reacts to the user picking a
// different profile, and exposes refresh() so other pages can ask it to
// re-sync after they change what config.json says. It does not show the
// window or call refresh() itself — the caller (app.go) does that once
// every page is wired up, so the very first paint already reflects the
// active profile.
func setupHeaderbar(win *window, r *runner) (*headerbarCtl, error) {
	h := &headerbarCtl{
		combo:        win.ProfileCombo,
		masterSwitch: win.MasterSwitch,
		statusLabel:  win.StatusLabel,
		runner:       r,
		topWindow:    win.Window,
	}

	h.combo.Connect("changed", func() {
		if h.repopulating {
			return
		}
		id := h.combo.GetActiveID()
		// "" is the "Nenhum perfil" placeholder (shown only when no
		// profile is active) or an unpopulated combo; id == h.current
		// guards against a spurious re-fire settling on what was already
		// active. Neither is a real user choice to switch to.
		if id == "" || id == h.current {
			return
		}
		h.trySwitch(id)
	})

	// proxy on/off (unlike Enable) never touches a target: it is pure state
	// plus a daemon SIGHUP, so this can be a real switch instead of a
	// button behind a confirmation dialog. GTK3's "state-set" signature is
	// gboolean state-set(GtkSwitch*, gboolean state); returning true here
	// would suppress GTK's own default handling of the toggle, which is not
	// what we want — the switch should still flip visually, so this always
	// returns false and lets GTK apply the requested state itself.
	h.masterSwitch.Connect("state-set", func(_ *gtk.Switch, state bool) bool {
		if h.repopulating {
			return false
		}
		h.trySetMaster(state)
		return false
	})

	return h, nil
}

// refresh re-reads config.json and repaints the selector. Safe to call from
// the UI thread: the read happens inside a runner job, off the GTK main
// loop.
func (h *headerbarCtl) refresh() {
	h.runner.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		return func() { h.applyProfiles(pf, err) }
	})
}

// applyProfiles rebuilds the combo's entries from pf and selects whatever
// is active. Runs on the UI thread only, from a runner delivery.
func (h *headerbarCtl) applyProfiles(pf *proxy.ProfileFile, err error) {
	h.repopulating = true
	defer func() { h.repopulating = false }()

	h.combo.RemoveAll()

	if err != nil {
		// Same fallback as the old inert button: nothing to show, nothing
		// to pick.
		h.combo.Append("", "Nenhum perfil")
		h.combo.SetActiveID("")
		h.combo.SetSensitive(false)
		h.current = ""
		h.applyMasterState(false)
		return
	}

	names := visibleProfiles(pf)
	active := pf.ActiveProfile

	// The extra entry is only ever present when what is active is not a
	// saved profile: no profile at all, or the reserved "_current" slot.
	// Once a saved profile is active it IS the selection, so a second row
	// alongside it would just be a confusing, inert option to pick.
	if id, label, ok := headerbarExtraEntry(active); ok {
		h.combo.Append(id, label)
	}
	for _, name := range names {
		h.combo.Append(name, name)
	}

	h.combo.SetSensitive(len(names) > 0)
	h.current = active
	h.combo.SetActiveID(active)
	h.applyMasterState(active != "")
}

// applyMasterState paints the master switch and its label to match active,
// via masterLabel (window.go) so the two never disagree. Callers are
// responsible for the repopulating guard: applyProfiles is already inside
// one when it calls this, and trySetMaster's revert path sets its own.
func (h *headerbarCtl) applyMasterState(active bool) {
	h.masterSwitch.SetActive(active)
	h.statusLabel.SetLabel(masterLabel(active))
	h.statusLabel.SetTooltipText(masterLabel(active))
}

// trySetMaster runs app.On/app.Off off the UI thread in response to the
// user flipping the master switch. Like doEnable, EscalateNone is mandatory:
// On/Off never touch a target, but ReloadDaemon still goes through this
// Executor, and a blocking password prompt here would freeze the window
// with no way out.
//
// app.On("") failing is not a bug: it is the ordinary case of a machine
// where the user never activated anything, so there is no last_profile to
// restore. That gets the friendly message below and the switch snapped back
// to off, rather than being treated like an unexpected error.
func (h *headerbarCtl) trySetMaster(state bool) {
	h.runner.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		if state {
			_, err := app.On(statusDeps(), ex, "")
			return func() {
				if err != nil {
					// Revert BEFORE the dialog, not after: the modal
					// blocks on dlg.Run() until the user closes it, and
					// during that wait the switch must already show the
					// truth (off) rather than sitting on "Ativo" behind a
					// dialog that says activation failed — the window
					// would otherwise contradict itself to the user's
					// face for as long as the dialog stays open.
					h.revertMaster(false)
					h.showError("Nenhum perfil para ativar. Escolha um no seletor ao lado.")
					return
				}
				h.syncAfterMasterChange()
			}
		}
		_, err := app.Off(statusDeps(), ex)
		return func() {
			if err != nil {
				// Same ordering as the On branch above, for the same
				// reason: the switch must already reflect reality before
				// the blocking dialog opens.
				h.revertMaster(true)
				h.showError(fmt.Sprintf("erro ao desativar proxy: %s", err))
				return
			}
			h.syncAfterMasterChange()
		}
	})
}

// revertMaster snaps the switch back to a known-good position after a
// failed On/Off, guarded by repopulating so SetActive's own "state-set"
// does not re-enter trySetMaster.
//
// This works — unlike a same-shape revert called synchronously from inside
// a "state-set" handler — only because it always runs from a runner
// delivery (trySetMaster's return func()), strictly after the On/Off job's
// original SetActive/state-set round has already finished. GTK's
// gtk_switch_set_active() reasserts the originally-requested state right
// after every "state-set" handler returns false, so a revert attempted
// *inside* that same handler call gets silently overwritten back — see
// page_daemon.go's bridge switch, which hits exactly that trap with a
// synchronous (no I/O) confirmation and has to defer its revert with
// r.post for this same reason. Do not "simplify" that call into an inline
// SetActive to match this function; it does not stick.
func (h *headerbarCtl) revertMaster(active bool) {
	h.repopulating = true
	h.applyMasterState(active)
	h.repopulating = false
}

// syncAfterMasterChange re-reads config.json after a successful On/Off:
// both change ActiveProfile, which is what the profile selector and the
// Status page both show. onProfileChanged is the same Task 4 callback the
// selector itself uses (wired to statusPage.load in app.go).
func (h *headerbarCtl) syncAfterMasterChange() {
	h.refresh()
	if h.onProfileChanged != nil {
		h.onProfileChanged()
	}
}

// trySwitch reads config.json off the UI thread, then asks routeForSwitch
// (profileform.go, no build tag, unit tested) which of app.Enable's two
// reachable routes this selection is, and reacts accordingly. The route
// decision itself lives outside this GTK file on purpose — see
// routeForSwitch's doc comment for why getting it wrong is a security bug,
// not a cosmetic one, and why that means it must be reachable from
// `go test ./...` without a display.
//
// Route 1 (viaLocal explicitly requested) is not reachable from here: that
// is "proxy set --via-local", a different action than picking a profile.
func (h *headerbarCtl) trySwitch(name string) {
	h.runner.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		return func() {
			if err != nil {
				h.showError(fmt.Sprintf("erro ao carregar perfis: %s", err))
				h.revert()
				return
			}
			switch routeForSwitch(pf.ViaLocal) {
			case routeStateOnly:
				h.doEnable(name, false)
			case routeRewritesTargets:
				if !h.confirmRewrite(name) {
					h.revert()
					return
				}
				h.doEnable(name, true)
			}
		}
	})
}

// confirmRewrite asks before a route-3 switch: it is about to reinvoke the
// CLI as root and rewrite every target's own proxy configuration, which can
// prompt for the user's password. The dialog is modal and transient for the
// main window, following the same pattern as the Perfis page's remove
// confirmation (see confirmRemove in page_profiles.go).
func (h *headerbarCtl) confirmRewrite(name string) bool {
	dlg := gtk.MessageDialogNew(h.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO,
		"Trocar para %q vai reescrever a configuração de proxy em todos os alvos e pode pedir sua senha. Continuar?", name)
	dlg.SetModal(true)
	dlg.SetTransientFor(h.topWindow)
	resp := dlg.Run()
	dlg.Destroy()
	return resp == gtk.RESPONSE_YES
}

// doEnable runs app.Enable off the UI thread. includePrivileged tells it
// whether to also reinvoke the CLI under pkexec for the privileged targets
// via applyPrivileged (Task 1's free function): that call is skipped
// entirely for the pf.ViaLocal route, since that route must not touch any
// target, privileged included — writing there would tear down the daemon
// plumbing and put the upstream credential back into every tool's config
// file, exactly what pf.ViaLocal exists to avoid.
//
// EscalateNone is mandatory on the in-process Executor: a user-level target
// that needed root inside this process would block on a password prompt
// the GUI cannot show, freezing the window with no explanation. Privileged
// targets never go through this Executor — they go through applyPrivileged,
// whose separate pkexec call is the only sanctioned way this package
// escalates.
func (h *headerbarCtl) doEnable(name string, includePrivileged bool) {
	h.runner.submit(func() func() {
		var user, privileged []string
		if includePrivileged {
			var infos []selectedTargetInfo
			for _, t := range proxy.AllTargets() {
				infos = append(infos, selectedTargetInfo{
					Name:          t.Name(),
					Root:          t.RequiresRoot(),
					SessionScoped: t.SessionScoped(),
				})
			}
			user, privileged = splitSessionAware(infos)
		}
		// includePrivileged == false means the pf.ViaLocal route: Enable's
		// own Route 2 branch ignores targetNames entirely (it only flips
		// ActiveProfile and reloads the daemon), so user is left nil rather
		// than computed for no reason.

		ex := &proxy.Executor{Escalation: proxy.EscalateNone, Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
		res, err := app.Enable(statusDeps(), ex, name, user, false)

		var privOut string
		if err == nil && includePrivileged && len(privileged) > 0 {
			_, privOut = applyPrivileged(name, privileged)
		}

		return func() {
			if err != nil {
				h.showError(fmt.Sprintf("erro ao trocar de perfil: %s", err))
				h.revert()
				return
			}
			h.current = name
			// The master switch has to be repainted here, and forgetting it
			// was a real bug with a confusing symptom: activating a profile
			// from the selector left the switch showing "Inativo" while the
			// config said otherwise, so flipping it went down app.On("") —
			// "restore the last profile" — which on a machine that never had
			// one fails with "Nenhum perfil para ativar", right after the
			// user had just picked one. Restarting the GUI "fixed" it only
			// because a fresh applyProfiles reads the truth off disk.
			//
			// The repopulating guard is this caller's responsibility (see
			// applyMasterState): without it, SetActive fires "state-set" and
			// trySetMaster would run app.On on top of the enable that just
			// finished.
			h.repopulating = true
			h.applyMasterState(true)
			h.repopulating = false

			h.showEnableResult(res, privOut)
			if h.onProfileChanged != nil {
				h.onProfileChanged()
			}
		}
	})
}

// showEnableResult surfaces what Enable actually did. Route 2
// (TargetsUntouched, no privileged output) has nothing worth a dialog for —
// that silence is the point, it is what makes it feel instantaneous.
// Route 3 reuses the same showApplyResultDialog the Status page's apply()
// uses, for the same reason: there is no separate vocabulary for "this
// Apply happened to be triggered by the profile selector".
func (h *headerbarCtl) showEnableResult(res app.EnableResult, privOut string) {
	if res.Report == nil && privOut == "" {
		return
	}
	onRestartDocker := restartDockerAction(h.topWindow, h.runner)
	if err := showApplyResultDialog(h.topWindow, res.Report, "", privOut, onRestartDocker); err != nil {
		h.showError(fmt.Sprintf("erro ao abrir resultado: %s", err))
	}
}

// revert restores the combo to h.current, the last profile it actually
// settled on — used when the user declines the route-3 confirmation, or
// when Enable itself fails, so the selector does not keep showing a
// profile that was never actually switched to. Guarded by repopulating for
// the same reason applyProfiles is: SetActiveID fires "changed" too.
func (h *headerbarCtl) revert() {
	h.repopulating = true
	h.combo.SetActiveID(h.current)
	h.repopulating = false
}

// showError reports a failure the user needs to see (config.json unreadable,
// Enable itself erroring out) via a modal dialog transient for the main
// window, matching the rest of this package's dialog conventions.
func (h *headerbarCtl) showError(msg string) {
	dlg := gtk.MessageDialogNew(h.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_ERROR, gtk.BUTTONS_OK, "%s", msg)
	dlg.SetModal(true)
	dlg.SetTransientFor(h.topWindow)
	dlg.Run()
	dlg.Destroy()
}
