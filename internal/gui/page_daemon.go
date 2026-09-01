//go:build gui

package gui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"time"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// Exact Portuguese strings from the task brief — kept as named constants so
// the confirmation dialogs and the warning under the switch cannot drift
// from what setupDaemonPage wires them to.
const (
	daemonBridgeSwitchLabel = "Escutar também na bridge do Docker"
	daemonBridgeWarning     = "Permite que QUALQUER container da máquina use o proxy."
	daemonBridgeConfirm     = "Ligar a bridge do Docker faz o proxy escutar também no gateway do Docker, e qualquer container da máquina passa a poder usá-lo. Continuar?"
	daemonRemoveConfirm     = "Remover o serviço? O proxy local para de rodar, e os alvos que apontam para ele deixam de funcionar."
	// daemonBridgeUnavailableTooltip explains an insensitive switch: a
	// machine with no docker0 interface is an environment fact, not an
	// error, so DockerBridgeAddr failing here disables the control instead
	// of surfacing a scary message. See load()'s doc comment.
	daemonBridgeUnavailableTooltip = "bridge do Docker não está disponível nesta máquina"
)

// daemonPage owns the Daemon page's "Serviço" block: the read-only state
// labels, the port and Docker bridge controls, and install/apply/remove/
// reload. The logs table (Task 4) is not built here.
type daemonPage struct {
	runner    *runner
	topWindow *gtk.Window

	stateLbl   *gtk.Label
	listenLbl  *gtk.Label
	profileLbl *gtk.Label
	// portSpin, not an Entry: the port is never optional here (unlike the
	// Perfis form's Port field, which can be blank), always has a value, and
	// must be a valid 1-65535 integer — a SpinButton enforces the range at
	// the widget level and GetValueAsInt() hands back an int with no manual
	// parsing/validation step, unlike profileform.go's port handling which
	// has to tolerate "" and reject non-numeric text by hand.
	portSpin *gtk.SpinButton

	bridgeSwitch  *gtk.Switch
	bridgeWarnLbl *gtk.Label

	reloadBtn  *gtk.Button
	removeBtn  *gtk.Button
	primaryBtn *gtk.Button
	resultLbl  *gtk.Label

	// staleTargetsBtn appears only after a port change left the targets on
	// the old port. It navigates to the Status page instead of applying
	// here: applying needs the target selection, the pkexec path and the
	// report display, all of which live on that page. Duplicating them
	// would fork the apply logic; reaching into statusPage would couple two
	// pages that today know nothing of each other.
	staleTargetsBtn *gtk.Button
	// stack is the window's page switcher, held only so the button above
	// can bring the user to "status".
	stack *gtk.Stack

	// pendingResult survives the load() that every action fires on its way
	// out. load() is asynchronous and its callback (applyLoad) clears
	// resultLbl, so a message written before it lands is wiped a moment
	// later — which is why "alterações aplicadas" only ever flickered.
	// applyLoad shows this instead of clearing, then empties it, so the
	// next plain reload still starts clean.
	pendingResult string

	// installed mirrors what load() last found on disk (serve.UnitPath()
	// exists). Read by refreshActionState and daemonPrimaryActionLabel; not
	// read from a widget because there is no widget for it — this page has
	// no checkbox for "installed", it is a fact load() discovers.
	installed bool
	// baseline is the port/bridge pair load() last populated the widgets
	// with, i.e. what is actually on disk. daemonPending compares it against
	// readForm() to decide whether the primary button has anything to send —
	// see the task brief's "decisão de projeto": the bridge switch changes
	// intent, not the system, until the primary button is pressed.
	baseline daemonFormValues

	// repopulating is true while load()/revert code sets the bridge switch's
	// Active state programmatically. GtkSwitch fires "state-set" for
	// SetActive exactly as it does for a real user drag — without this guard
	// a revert-after-declined-confirmation would recurse into the same
	// handler and try to confirm the revert itself. Same pattern as
	// headerbar.go's repopulating and page_profiles.go's restoringSelection.
	repopulating bool

	// busy mirrors the runner's busy state (set by the busy handler wired in
	// setupDaemonPage), read by refreshActionState so a job in flight can
	// force the primary button off regardless of what the (still enabled,
	// not disabled during a job — same reasoning as page_profiles.go) port
	// field or bridge switch currently say.
	busy bool

	// Logs block (Task 4). hostEntry/errorsChk/directChk are the filters;
	// logStore backs the table and logView/logScroll show it.
	// logsResultLbl carries this block's own errors, separate from
	// resultLbl (the service block's), so a failed journal read does not
	// stomp on or get stomped by an install/apply message.
	logsHostEntry  *gtk.Entry
	logsErrorsChk  *gtk.CheckButton
	logsDirectChk  *gtk.CheckButton
	logsRefreshBtn *gtk.Button
	logsClearBtn   *gtk.Button
	logsLiveChk    *gtk.CheckButton
	logsResultLbl  *gtk.Label

	logStore  *gtk.ListStore
	logView   *gtk.TreeView
	logScroll *gtk.ScrolledWindow

	// lastRows is the table's current content, so applyLogs can skip a
	// rebuild when a live tick brought nothing new — see sameRows.
	lastRows []logRow

	// liveStop closes to end the live-refresh goroutine. Non-nil exactly
	// while that goroutine is running, which is what makes setLive
	// idempotent in both directions.
	liveStop chan struct{}

	// liveWanted and windowShown are the two independent conditions the
	// ticker needs: what the user asked for with the "Ao vivo" checkbox,
	// and whether the window is actually on screen. They are kept apart so
	// that reappearing from the tray restores the ticker only for a user
	// who wanted it — folding them into one flag would silently re-arm it
	// for someone who had deliberately unchecked the box. syncLive is the
	// only thing that turns the pair into a decision.
	liveWanted  bool
	windowShown bool

	// logsInFlight counts refreshes submitted but not yet delivered back.
	// The live tick fires on wall-clock time with no idea whether the
	// previous read finished; on a slow journal read every tick would pile
	// one more job onto the runner's bounded queue until submitQuiet — a
	// send on a full channel, made from the UI thread — blocked the main
	// loop. Quiet refreshes are simply skipped while one is in flight.
	// UI thread only.
	logsInFlight int
}

// setupDaemonPage fills win.DaemonPage (built empty by newWindow) with the
// service block described by the task brief: a formGrid of read-only rows
// (Estado/Escutando/Perfil) plus the Porta control, the bridge switch and
// its warning, and the install/apply/remove/reload buttons.
func setupDaemonPage(win *window, r *runner) (*daemonPage, error) {
	dp := &daemonPage{runner: r, topWindow: win.Window}

	title, err := sectionTitle("Serviço")
	if err != nil {
		return nil, err
	}
	win.DaemonPage.PackStart(title, false, false, 0)

	grid, err := formGrid()
	if err != nil {
		return nil, err
	}
	win.DaemonPage.PackStart(grid, false, false, 0)

	stateVal, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	stateVal.SetXAlign(0)
	if _, err := addGridRow(grid, 0, "Estado", stateVal); err != nil {
		return nil, err
	}
	dp.stateLbl = stateVal

	listenVal, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	listenVal.SetXAlign(0)
	if _, err := addGridRow(grid, 1, "Escutando", listenVal); err != nil {
		return nil, err
	}
	dp.listenLbl = listenVal

	profileVal, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	profileVal.SetXAlign(0)
	if _, err := addGridRow(grid, 2, "Perfil", profileVal); err != nil {
		return nil, err
	}
	dp.profileLbl = profileVal

	portSpin, err := gtk.SpinButtonNewWithRange(1, 65535, 1)
	if err != nil {
		return nil, err
	}
	if _, err := addGridRow(grid, 3, "Porta", portSpin); err != nil {
		return nil, err
	}
	dp.portSpin = portSpin
	portSpin.Connect("value-changed", func() { dp.refreshActionState() })

	bridgeBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
	if err != nil {
		return nil, err
	}
	bridgeBox.SetMarginTop(spaceRelated)

	bridgeSwitch, err := gtk.SwitchNew()
	if err != nil {
		return nil, err
	}
	dp.bridgeSwitch = bridgeSwitch
	// state-set, not "changed" or "notify::active": this is the same signal
	// headerbar.go's masterSwitch uses for a confirm-before-flip control.
	// gotk3's Switch has no SetState/GetState (only SetActive/GetActive —
	// see go doc), so unlike the "return TRUE and call set_state yourself"
	// pattern the C docs describe, this codebase's established approach
	// (headerbar.go) is: let the flip happen (return false always), then
	// undo it with a guarded SetActive if the user declines.
	//
	// The revert itself CANNOT run synchronously inside this handler, unlike
	// what a first reading of headerbar.go suggests: gtk_switch_set_active's
	// own C implementation reasserts the ORIGINAL requested state right
	// after every "state-set" handler returns false (that is how "return
	// false lets GTK apply the requested state" is implemented), so a
	// SetActive(false) called here, before this handler returns, gets
	// silently overwritten back to true the instant this handler's own
	// "return false" is processed — confirmed by a widget-tree probe run for
	// this task, which saw the switch end up active again with a genuine
	// decline. headerbar.go's own revertMaster gets away with the naive
	// version only because its revert runs from a runner delivery, strictly
	// after the original SetActive call (and GTK's reassertion) has already
	// finished — r.post here buys the exact same "after, not during" timing
	// without needing I/O to justify a full submit() job. r.post, not
	// glib.IdleAdd directly: jobs.go is the package's only sanctioned
	// boundary to GTK's main loop (it has no build tag and imports no
	// glib — see its own doc comment), and every direct glib.IdleAdd call
	// scattered outside that boundary erodes the one place that keeps the
	// runner testable without a display.
	bridgeSwitch.Connect("state-set", func(_ *gtk.Switch, state bool) bool {
		if dp.repopulating {
			return false
		}
		if state && !dp.confirmBridgeOn() {
			dp.runner.post(func() {
				dp.repopulating = true
				dp.bridgeSwitch.SetActive(false)
				dp.repopulating = false
				dp.refreshActionState()
			})
			return false
		}
		dp.refreshActionState()
		return false
	})
	bridgeBox.PackStart(bridgeSwitch, false, false, 0)

	bridgeLbl, err := gtk.LabelNew(daemonBridgeSwitchLabel)
	if err != nil {
		return nil, err
	}
	bridgeLbl.SetXAlign(0)
	bridgeBox.PackStart(bridgeLbl, false, false, 0)

	win.DaemonPage.PackStart(bridgeBox, false, false, 0)

	bridgeWarnLbl, err := gtk.LabelNew(daemonBridgeWarning)
	if err != nil {
		return nil, err
	}
	bridgeWarnLbl.SetXAlign(0)
	// Indented under the switch's label, not the switch itself, and dimmed
	// like the Perfis page's explanatory labels (page_profiles.go's
	// explainLbl) — it is a caption on the control above, not a fact of its
	// own weight.
	bridgeWarnLbl.SetMarginStart(spaceSection)
	if ctx, err := bridgeWarnLbl.GetStyleContext(); err == nil {
		ctx.AddClass("dim-label")
	}
	win.DaemonPage.PackStart(bridgeWarnLbl, false, false, 0)
	dp.bridgeWarnLbl = bridgeWarnLbl

	buttons, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceTight)
	if err != nil {
		return nil, err
	}
	buttons.SetHAlign(gtk.ALIGN_END)
	buttons.SetMarginTop(spaceSection)

	reloadBtn, err := gtk.ButtonNewWithLabel("Recarregar")
	if err != nil {
		return nil, err
	}
	reloadBtn.Connect("clicked", func() { dp.reload() })
	buttons.PackStart(reloadBtn, false, false, 0)
	dp.reloadBtn = reloadBtn

	removeBtn, err := gtk.ButtonNewWithLabel("Remover serviço")
	if err != nil {
		return nil, err
	}
	// Hidden (not merely insensitive) until load() finds an installed unit:
	// there is nothing to remove otherwise. SetNoShowAll(true) BEFORE
	// SetVisible(false) — app.go's Run calls win.Window.ShowAll() once,
	// after every page is set up, and ShowAll forces every descendant
	// visible regardless of an earlier SetVisible(false). Already bit this
	// project twice (page_profiles.go's removeBtn, page_status.go's
	// "Recarregar com sudo").
	removeBtn.SetNoShowAll(true)
	removeBtn.SetVisible(false)
	removeBtn.Connect("clicked", func() { dp.confirmAndRemove() })
	buttons.PackStart(removeBtn, false, false, 0)
	dp.removeBtn = removeBtn

	// Packed end LAST, per the same convention as page_profiles.go's
	// primaryBtn: PackEnd stacks each new call closer to the true end than
	// the ones before it, putting this in the rightmost, primary-action
	// position the brief's layout shows.
	primaryBtn, err := gtk.ButtonNewWithLabel(daemonPrimaryActionLabel(false))
	if err != nil {
		return nil, err
	}
	primaryBtn.Connect("clicked", func() { dp.applyPrimary() })
	buttons.PackStart(primaryBtn, false, false, 0)
	dp.primaryBtn = primaryBtn

	win.DaemonPage.PackStart(buttons, false, false, 0)

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return nil, err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	win.DaemonPage.PackStart(resultLbl, false, false, 0)
	dp.resultLbl = resultLbl

	staleTargetsBtn, err := gtk.ButtonNewWithLabel("Reaplicar alvos")
	if err != nil {
		return nil, err
	}
	staleTargetsBtn.SetHAlign(gtk.ALIGN_START)
	staleTargetsBtn.SetNoShowAll(true)
	staleTargetsBtn.SetVisible(false)
	staleTargetsBtn.Connect("clicked", func() {
		dp.stack.SetVisibleChildName("status")
		dp.setStaleTargetsBtnVisible(false)
	})
	win.DaemonPage.PackStart(staleTargetsBtn, false, false, 0)
	dp.staleTargetsBtn = staleTargetsBtn
	dp.stack = win.Stack

	if err := dp.setupLogsBlock(win); err != nil {
		return nil, err
	}

	// Disable the buttons that submit a job while one is already running —
	// same reasoning as page_status.go/page_profiles.go: the runner is
	// one-job-at-a-time, and a click landing mid-install would just queue up
	// behind it with a stale view of the form.
	r.setBusyHandler(func(busy bool) {
		// dp.busy set BEFORE refreshActionState, not after: that call reads
		// it to decide the primary button's state.
		dp.busy = busy
		enabled := !busy
		dp.reloadBtn.SetSensitive(enabled)
		dp.removeBtn.SetSensitive(enabled)
		dp.logsRefreshBtn.SetSensitive(enabled)
		dp.logsClearBtn.SetSensitive(enabled)
		dp.refreshActionState()
	})

	// Polling the journal every two seconds while nobody can see the table
	// is pure waste. Two ways off screen, two signals: "hide" fires on the
	// Window.Hide() that window.go's delete-event handler calls for
	// close-to-tray (and "show" on the Present() the tray's "Abrir" does);
	// iconifying emits no hide/show at all, only "window-state-event" with
	// the ICONIFIED bit flipping.
	win.Window.Connect("hide", func() {
		dp.windowShown = false
		dp.syncLive()
	})
	win.Window.Connect("show", func() {
		dp.windowShown = true
		dp.syncLive()
		dp.refreshOnReappear()
	})
	win.Window.Connect("window-state-event", func(_ *gtk.Window, ev *gdk.Event) {
		e := gdk.EventWindowStateNewFromEvent(ev)
		if e.ChangedMask()&gdk.WINDOW_STATE_ICONIFIED == 0 {
			return
		}
		iconified := e.NewWindowState()&gdk.WINDOW_STATE_ICONIFIED != 0
		dp.windowShown = !iconified
		dp.syncLive()
		if !iconified {
			dp.refreshOnReappear()
		}
	})

	dp.load()
	// Quiet: the window is still being built, and there is no click to
	// acknowledge.
	dp.refreshLogs(true)
	// The window has not been shown at this point — app.go calls ShowAll
	// after every page is set up — so assume it is about to be, and let
	// "hide"/"show"/"window-state-event" correct it from here on. This
	// syncLive is what starts the ticker for a user whose logs_live was
	// restored as on; for everyone else it is a no-op.
	dp.windowShown = true
	dp.syncLive()

	return dp, nil
}

// readForm captures the port/bridge widgets' current values. UI thread only.
func (dp *daemonPage) readForm() daemonFormValues {
	return daemonFormValues{
		Port:         dp.portSpin.GetValueAsInt(),
		DockerBridge: dp.bridgeSwitch.GetActive(),
	}
}

// refreshActionState recomputes the primary button's sensitivity: always on
// while not installed (the first install has nothing to compare against —
// clicking it is the point), and on only while there is a pending change
// once installed (daemonPending against dp.baseline) — the switch/port do
// not apply themselves, per the task brief's decision that a click on the
// primary button is the only thing that reinstalls the service.
func (dp *daemonPage) refreshActionState() {
	if dp.busy {
		dp.primaryBtn.SetSensitive(false)
		return
	}
	if !dp.installed {
		dp.primaryBtn.SetSensitive(true)
		return
	}
	dp.primaryBtn.SetSensitive(daemonPending(dp.baseline, dp.readForm()))
}

// confirmBridgeOn asks before turning the bridge switch ON — never before
// turning it off, per the task brief. Purely a dialog, no I/O, so (like
// page_profiles.go's confirmDiscardChanges/confirmRemove) it runs
// synchronously on the UI thread rather than through the runner.
func (dp *daemonPage) confirmBridgeOn() bool {
	dlg := gtk.MessageDialogNew(dp.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO, "%s", daemonBridgeConfirm)
	dlg.SetModal(true)
	dlg.SetTransientFor(dp.topWindow)
	resp := dlg.Run()
	dlg.Destroy()
	return resp == gtk.RESPONSE_YES
}

// load re-reads the daemon's state off the UI thread: serve.DaemonActive(),
// whether the unit file exists, config.json (port, bridge preference, active
// profile) and, when a Docker bridge is reachable, its address. Called at
// setup and after every install/apply/remove/reload so the block reflects
// what is actually on disk/systemd, not what the form assumes happened.
//
// serve.DockerBridgeAddr's error is not treated as a failure: a machine with
// no docker0 interface (Docker never started, or not installed) is a normal
// environment, not a bug — see daemonBridgeUnavailableTooltip.
func (dp *daemonPage) load() {
	dp.runner.submit(func() func() {
		active := serve.DaemonActive()

		installed := false
		if path, err := serve.UnitPath(); err == nil {
			if _, statErr := os.Stat(path); statErr == nil {
				installed = true
			}
		}

		pf, err := proxy.LoadProfiles()
		if err != nil {
			return func() {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao carregar configuração: %s", err))
			}
		}

		bridgeAddr, bridgeErr := serve.DockerBridgeAddr()

		return func() { dp.applyLoad(active, installed, pf, bridgeAddr, bridgeErr) }
	})
}

// applyLoad renders a load() result onto the widgets. UI thread only, called
// from a runner delivery.
func (dp *daemonPage) applyLoad(active, installed bool, pf *proxy.ProfileFile, bridgeAddr string, bridgeErr error) {
	port := pf.EffectiveLocalPort()
	bridgeOn := pf.DockerBridge
	bridgeAvailable := bridgeErr == nil

	// Listen shows the bridge address only when it is both turned on AND
	// actually reachable — a saved preference for a machine that no longer
	// has Docker installed should not claim an address that is not really
	// being listened on.
	listenBridgeAddr := ""
	if bridgeOn && bridgeAvailable {
		listenBridgeAddr = bridgeAddr
	}
	sum := summarize(active, installed, port, listenBridgeAddr, pf.ActiveProfile)
	dp.stateLbl.SetText(sum.State)
	dp.listenLbl.SetText(sum.Listen)
	dp.profileLbl.SetText(sum.Profile)

	dp.installed = installed
	dp.portSpin.SetValue(float64(port))

	// Guarded: SetActive fires "state-set" like a real drag would, and
	// without repopulating this would run the confirm-on-ON logic on every
	// load(), including the very first one.
	dp.repopulating = true
	dp.bridgeSwitch.SetActive(bridgeOn)
	dp.repopulating = false

	if bridgeAvailable {
		dp.bridgeSwitch.SetSensitive(true)
		dp.bridgeSwitch.SetTooltipText("")
	} else {
		dp.bridgeSwitch.SetSensitive(false)
		dp.bridgeSwitch.SetTooltipText(daemonBridgeUnavailableTooltip)
	}

	// Set after the widgets above are (re)filled, not before — same
	// reasoning as page_profiles.go's fillForm/startNew: the widget updates
	// above fire "changed"/"state-set" too, and setting the baseline first
	// would make the primary button light up from load() itself repainting
	// the form to match what it just read.
	dp.baseline = daemonFormValues{Port: port, DockerBridge: bridgeOn}

	dp.primaryBtn.SetLabel(daemonPrimaryActionLabel(installed))
	dp.removeBtn.SetVisible(installed)

	dp.resultLbl.SetText(dp.pendingResult)
	dp.pendingResult = ""
	dp.refreshActionState()
}

// applyPrimary installs the service (not yet installed) or reinstalls it
// with the pending port/bridge changes (already installed) — InstallUnit
// itself only restarts a running unit, so this one call correctly covers
// both "Instalar serviço" and "Aplicar alterações". Port and bridge are read
// on the UI thread, right before submit, and captured — reading a widget
// from the runner's worker goroutine is the bug page_status.go's apply()
// doc comment already warns about.
func (dp *daemonPage) applyPrimary() {
	form := dp.readForm()
	wasInstalled := dp.installed

	dp.runner.submit(func() func() {
		execPath, err := os.Executable()
		if err != nil {
			return func() {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao resolver o próprio binário: %s", err))
			}
		}

		// Port and bridge are persisted BEFORE InstallUnit, under
		// WithProfileLock, outside of it — flock does not nest within this
		// process (see doRemove's doc comment in page_profiles.go for the
		// same rule applied to ReloadDaemon). Writing the port first also
		// mirrors cmd/proxy_serve.go's "install" command: --via-local and
		// "proxy status" read LocalPort from disk, so a unit installed
		// before the new port is saved would briefly disagree with them.
		// Captured inside the lock, before the overwrite: the old port is
		// what every target still points at, and it is gone the moment the
		// new one is saved. Reading it afterwards would always report "no
		// change".
		var warning string
		if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
			if portChangeStrandsTargets(lpf.EffectiveLocalPort(), form.Port, lpf.ViaLocal) {
				warning = staleTargetsWarning(lpf.EffectiveLocalPort(), form.Port)
			}
			lpf.LocalPort = form.Port
			lpf.DockerBridge = form.DockerBridge
			return nil
		}); err != nil {
			return func() {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao salvar configuração: %s", err))
			}
		}

		// EscalateNone: InstallUnit only ever runs "systemctl --user ...",
		// which never needs root. A blocking password prompt here would
		// freeze the window with no way out; failing explicitly instead
		// (per the task brief) surfaces the problem instead of hanging.
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		if err := serve.InstallUnit(ex, execPath, form.Port, form.DockerBridge); err != nil {
			return func() {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao instalar serviço: %s", err))
			}
		}

		return func() {
			// Through pendingResult, not SetText: dp.load() below would
			// clear the label before the user could read it.
			dp.pendingResult = daemonApplyMessage(wasInstalled, warning)
			dp.setStaleTargetsBtnVisible(warning != "")
			dp.load()
		}
	})
}

// confirmAndRemove asks for confirmation, modal and transient for the main
// window like every other destructive dialog in this package, then removes
// the service.
// setStaleTargetsBtnVisible shows or hides the "Reaplicar alvos" button.
// SetNoShowAll is set on the widget, so a ShowAll() elsewhere on this page
// cannot make it reappear on its own once hidden.
func (dp *daemonPage) setStaleTargetsBtnVisible(visible bool) {
	if dp.staleTargetsBtn == nil {
		return
	}
	dp.staleTargetsBtn.SetVisible(visible)
}

func (dp *daemonPage) confirmAndRemove() {
	dlg := gtk.MessageDialogNew(dp.topWindow, gtk.DIALOG_MODAL, gtk.MESSAGE_QUESTION, gtk.BUTTONS_YES_NO, "%s", daemonRemoveConfirm)
	dlg.SetModal(true)
	dlg.SetTransientFor(dp.topWindow)
	resp := dlg.Run()
	dlg.Destroy()
	if resp != gtk.RESPONSE_YES {
		return
	}
	dp.remove()
}

// remove uninstalls the systemd unit off the UI thread. EscalateNone for the
// same reason as applyPrimary: systemctl --user never needs root.
func (dp *daemonPage) remove() {
	dp.runner.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		err := serve.UninstallUnit(ex)
		return func() {
			if err != nil {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao remover serviço: %s", err))
				return
			}
			dp.resultLbl.SetText("serviço removido")
			dp.load()
		}
	})
}

// reload asks a running daemon to re-read its config via SIGHUP
// (serve.ReloadDaemon). A no-op when the daemon is not running, so this
// button is left visible and sensitive regardless of installed state.
func (dp *daemonPage) reload() {
	dp.runner.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		err := serve.ReloadDaemon(ex)
		return func() {
			if err != nil {
				dp.resultLbl.SetText(fmt.Sprintf("erro ao recarregar: %s", err))
				return
			}
			dp.resultLbl.SetText("serviço recarregado")
			dp.load()
		}
	})
}

// logModel column indices, in the order ListStoreNew is given the types
// below. The six visible columns come first so TreeViewColumnNewWithAttribute
// can bind to them by the same index logRow's fields fill; the two sort keys
// are appended after so no visible column's index shifts if a sort key is
// ever added or removed.
const (
	logColTime = iota
	logColMethod
	logColTarget
	logColRoute
	logColStatus
	logColDuration
	logColSortTime
	logColSortDuration
)

// setupLogsBlock builds the "Logs" section: the filter row (Host, Só erros,
// Só diretos, Resumo, Atualizar) and the table/summary pair described in the
// task brief. Called from setupDaemonPage, after the service block's own
// widgets are packed.
func (dp *daemonPage) setupLogsBlock(win *window) error {
	title, err := sectionTitle("Logs")
	if err != nil {
		return err
	}
	title.SetMarginTop(spaceSection)
	win.DaemonPage.PackStart(title, false, false, 0)

	filterBox, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, spaceRelated)
	if err != nil {
		return err
	}

	hostLbl, err := gtk.LabelNew("Host")
	if err != nil {
		return err
	}
	filterBox.PackStart(hostLbl, false, false, 0)

	hostEntry, err := gtk.EntryNew()
	if err != nil {
		return err
	}
	hostEntry.Connect("activate", func() { dp.refreshLogs(false) })
	filterBox.PackStart(hostEntry, false, false, 0)
	dp.logsHostEntry = hostEntry

	errorsChk, err := gtk.CheckButtonNewWithLabel("Só erros")
	if err != nil {
		return err
	}
	errorsChk.Connect("toggled", func() { dp.refreshLogs(false) })
	filterBox.PackStart(errorsChk, false, false, 0)
	dp.logsErrorsChk = errorsChk

	directChk, err := gtk.CheckButtonNewWithLabel("Só diretos")
	if err != nil {
		return err
	}
	directChk.Connect("toggled", func() { dp.refreshLogs(false) })
	filterBox.PackStart(directChk, false, false, 0)
	dp.logsDirectChk = directChk

	refreshBtn, err := gtk.ButtonNewWithLabel("Atualizar")
	if err != nil {
		return err
	}
	refreshBtn.Connect("clicked", func() { dp.refreshLogs(false) })
	filterBox.PackStart(refreshBtn, false, false, 0)
	dp.logsRefreshBtn = refreshBtn

	liveChk, err := gtk.CheckButtonNewWithLabel("Ao vivo")
	if err != nil {
		return err
	}
	liveChk.SetTooltipText("Relê o journal a cada 2 segundos enquanto marcado.")
	// Off by default, restored from config.json's logs_live: the live tick
	// is a cost the user opts into once, not a mode they have to re-enable
	// every launch. Like buildUI's close_to_tray read, a read failure
	// falls back to the safe default (off). SetActive runs BEFORE the
	// toggled handler is connected, so restoring never re-writes the
	// config or starts the ticker early — the setup tail's syncLive does
	// that, after windowShown is decided.
	logsLive := false
	if pf, err := proxy.LoadProfiles(); err == nil {
		logsLive = pf.LogsLive
	}
	liveChk.SetActive(logsLive)
	dp.liveWanted = logsLive
	liveChk.Connect("toggled", func() {
		active := liveChk.GetActive()
		dp.liveWanted = active
		dp.syncLive()
		// Persist like tray.go's close_to_tray: I/O off the GTK thread,
		// inside a submitted job. Quiet — a checkbox toggle needs no busy
		// signal, and flickering every button over a one-field write would
		// be all a loud job bought.
		dp.runner.submitQuiet(func() func() {
			_ = proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
				pf.LogsLive = active
				return nil
			})
			return nil
		})
	})
	filterBox.PackStart(liveChk, false, false, 0)
	dp.logsLiveChk = liveChk

	clearBtn, err := gtk.ButtonNewWithLabel("Limpar")
	if err != nil {
		return err
	}
	clearBtn.SetTooltipText("Esconde as entradas anteriores a agora. Nada é apagado do journal — \"proxy logs --all\" mostra tudo de novo.")
	clearBtn.Connect("clicked", func() { dp.clearLogs() })
	filterBox.PackStart(clearBtn, false, false, 0)
	dp.logsClearBtn = clearBtn

	win.DaemonPage.PackStart(filterBox, false, false, 0)

	// The table. Six string columns for the visible cells plus two hidden
	// int64 columns (SortTime, SortDuration) so the Hora/Duração columns can
	// sort on the numeric key instead of the formatted text — sorting
	// "12 ms"/"9 ms" as text would put "12 ms" first.
	store, err := gtk.ListStoreNew(
		glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_STRING,
		glib.TYPE_STRING, glib.TYPE_STRING, glib.TYPE_STRING,
		glib.TYPE_INT64, glib.TYPE_INT64,
	)
	if err != nil {
		return err
	}
	dp.logStore = store

	view, err := gtk.TreeViewNewWithModel(store)
	if err != nil {
		return err
	}
	view.SetHeadersClickable(true)

	columns := []struct {
		title   string
		dataCol int
		sortCol int
	}{
		{"Hora", logColTime, logColSortTime},
		{"Método", logColMethod, logColMethod},
		{"Destino", logColTarget, logColTarget},
		{"Rota", logColRoute, logColRoute},
		{"Status", logColStatus, logColStatus},
		{"Duração", logColDuration, logColSortDuration},
	}
	for _, c := range columns {
		renderer, err := gtk.CellRendererTextNew()
		if err != nil {
			return err
		}
		col, err := gtk.TreeViewColumnNewWithAttribute(c.title, renderer, "text", c.dataCol)
		if err != nil {
			return err
		}
		col.SetResizable(true)
		// SetSortColumnID is what makes the header clickable-to-sort AND
		// what picks which model column that click sorts by. Pointing
		// "Duração" at logColSortDuration (not logColDuration, its own text
		// column) is the whole reason logRow carries SortDuration at all —
		// see logRow's doc comment.
		col.SetSortColumnID(c.sortCol)
		view.AppendColumn(col)
	}
	// Newest first: a log table is read from the top, and the interesting
	// entry is the one that just arrived. This is the model's default sort,
	// so it survives every rebuild; clicking a header still re-sorts.
	store.SetSortColumnId(logColSortTime, gtk.SORT_DESCENDING)

	dp.logView = view

	scroll, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		return err
	}
	scroll.SetPolicy(gtk.POLICY_AUTOMATIC, gtk.POLICY_AUTOMATIC)
	scroll.SetVExpand(true)
	scroll.Add(view)
	win.DaemonPage.PackStart(scroll, true, true, 0)
	dp.logScroll = scroll

	resultLbl, err := gtk.LabelNew("")
	if err != nil {
		return err
	}
	resultLbl.SetXAlign(0)
	resultLbl.SetLineWrap(true)
	win.DaemonPage.PackStart(resultLbl, false, false, 0)
	dp.logsResultLbl = resultLbl

	return nil
}

// refreshLogs reads the filter widgets (UI thread only, before submit) and
// re-reads the journal off the UI thread: journalctl -> serve.ParseEntries
// -> serve.Filter -> logRows for the table.
// quiet suppresses the runner's busy signal. A refresh the user clicked for
// should disable the buttons — that is how they know the click landed — but
// the two-second live tick must not, or every button in the window flickers
// on a two-second cycle, on every page.
func (dp *daemonPage) refreshLogs(quiet bool) {
	// A tick that lands while the previous refresh is still in flight has
	// nothing to add — the running read will deliver the same tail. A loud
	// click still queues: the busy signal is the user's acknowledgement.
	if quiet && dp.logsInFlight > 0 {
		return
	}
	host, err := dp.logsHostEntry.GetText()
	if err != nil {
		host = ""
	}
	opts := serve.FilterOptions{
		Host:       host,
		ErrorsOnly: dp.logsErrorsChk.GetActive(),
		DirectOnly: dp.logsDirectChk.GetActive(),
	}

	submit := dp.runner.submit
	if quiet {
		submit = dp.runner.submitQuiet
	}
	dp.logsInFlight++
	submit(func() func() {
		done := dp.refreshLogsWork(opts)
		return func() {
			dp.logsInFlight--
			done()
		}
	})
}

// refreshLogsWork is refreshLogs' off-UI-thread half: it runs the journal
// read and returns the closure that renders the result. Split out so the
// submit wrapper in refreshLogs can pair every logsInFlight++ with exactly
// one -- on delivery, whichever of the return paths below fires.
func (dp *daemonPage) refreshLogsWork(opts serve.FilterOptions) func() {
	// The cut-off set by Limpar (and by "proxy logs clear") lives in the
	// config, so it is read here, off the UI thread, on every refresh —
	// otherwise Limpar would write it and leave the table full.
	cutoff := ""
	if pf, err := proxy.LoadProfiles(); err == nil {
		cutoff = pf.LogsSince
	}

	cmd := exec.Command("journalctl", logsJournalArgs(cutoff)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return func() {
			dp.logsResultLbl.SetText(fmt.Sprintf("erro ao ler o journal: %s", msg))
		}
	}

	entries, err := serve.ParseEntries(bytes.NewReader(out))
	if err != nil {
		return func() {
			dp.logsResultLbl.SetText(fmt.Sprintf("erro ao interpretar o journal: %s", err))
		}
	}

	rows := logRows(serve.Filter(entries, opts))

	return func() { dp.applyLogs(rows) }
}

// applyLogs renders a refreshLogs result onto the table. UI thread only,
// called from a runner delivery.
func (dp *daemonPage) applyLogs(rows []logRow) {
	// Rebuilding drops the selection and the scroll position. At one tick
	// every two seconds, a quiet proxy would do that forever for nothing.
	if dp.lastRows != nil && sameRows(dp.lastRows, rows) {
		dp.logsResultLbl.SetText("")
		return
	}
	dp.lastRows = rows

	dp.logStore.Clear()
	for _, row := range rows {
		iter := dp.logStore.Append()
		if err := dp.logStore.Set(iter,
			[]int{logColTime, logColMethod, logColTarget, logColRoute, logColStatus, logColDuration, logColSortTime, logColSortDuration},
			[]interface{}{row.Time, row.Method, row.Target, row.Route, row.Status, row.Duration, row.SortTime, row.SortDuration},
		); err != nil {
			dp.logsResultLbl.SetText(fmt.Sprintf("erro ao preencher a tabela: %s", err))
			return
		}
	}

	dp.logsResultLbl.SetText("")
}

// clearLogs starts the log view over by recording "read from now on" in the
// config, then re-reading. It deletes nothing, and the button's tooltip says
// so: journalctl cannot drop a single unit's entries — its --vacuum-* flags
// act on journal FILES and ignore -u — so a real delete would take every
// other user unit's logs with it. "proxy logs --all" is the way back.
//
// The write is I/O and takes the profile lock, so it goes through the
// runner; refreshLogs runs from the delivery, on the UI thread.
func (dp *daemonPage) clearLogs() {
	dp.runner.submit(func() func() {
		now := time.Now().Format(time.RFC3339)
		if err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			pf.LogsSince = now
			return nil
		}); err != nil {
			return func() {
				dp.logsResultLbl.SetText(fmt.Sprintf("erro ao limpar os logs: %s", err))
			}
		}
		return func() {
			dp.logsResultLbl.SetText("")
			// Loud: the user clicked Limpar and is waiting to see the
			// table empty.
			dp.refreshLogs(false)
		}
	})
}

// liveInterval is how often the live refresh re-reads the journal. Two
// seconds is short enough that a request feels like it appeared on its own
// and long enough that a journalctl call every tick costs nothing worth
// measuring.
const liveInterval = 2 * time.Second

// syncLive turns the two conditions — the user's "Ao vivo" checkbox and
// whether the window is on screen — into one decision. Every caller flips
// its own flag and then calls this, so neither condition can win over the
// other by accident. UI thread only.
func (dp *daemonPage) syncLive() {
	dp.setLive(dp.liveWanted && dp.windowShown)
}

// refreshOnReappear reads the journal once, right now, when the window comes
// back on screen with the live ticker armed — not in two seconds. Every
// refresh is a full re-read of the journal's tail, so nothing that happened
// while off screen is lost, but waiting for the first tick would show a
// stale table at exactly the moment the user is looking at it. UI thread
// only.
func (dp *daemonPage) refreshOnReappear() {
	if dp.liveStop != nil {
		dp.refreshLogs(true)
	}
}

// setLive starts or stops the live refresh. Idempotent in both directions:
// liveStop is non-nil exactly while the goroutine runs, so a second "on"
// cannot start a second ticker and a second "off" cannot close a closed
// channel.
//
// The ticker lives OUTSIDE the runner on purpose. runner.run pulls one job
// at a time off a single goroutine, so anything long-running submitted into
// it starves every other job in the window — status reads, applies, profile
// switches. This goroutine only sleeps, then asks the UI thread (via
// runner.post, the sanctioned boundary — nothing outside jobs.go may call
// glib.IdleAdd) to do what the Atualizar button does. The actual work still
// goes through submit and stays serialized with everything else.
//
// UI thread only: it is called from the checkbox handler and from setup.
func (dp *daemonPage) setLive(on bool) {
	if on == (dp.liveStop != nil) {
		return
	}
	if !on {
		close(dp.liveStop)
		dp.liveStop = nil
		return
	}

	stop := make(chan struct{})
	dp.liveStop = stop
	go func() {
		ticker := time.NewTicker(liveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// post, not submit: refreshLogs reads the filter widgets,
				// which is only legal on the UI thread. It submits the I/O
				// itself.
				dp.runner.post(func() { dp.refreshLogs(true) })
			}
		}
	}()
}
