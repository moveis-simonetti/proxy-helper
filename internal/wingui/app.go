//go:build wingui

package wingui

import (
	_ "embed"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"proxy-helper/internal/proxy"
)

//go:embed icons/proxy-on.svg
var iconOnSVG []byte

//go:embed icons/proxy-off.svg
var iconOffSVG []byte

// appID must stay stable: Fyne uses it for the app's own storage.
const appID = "br.com.simonetti.msproxy"

// windowTitle is what the person sees, which is not what the binary is
// called.
const windowTitle = "MS Proxy"

// Run starts the interface. hidden starts it in the tray, which is how the
// logon task launches it: the window is the rare case, the tray is the
// product.
func Run(hidden bool) error {
	a := app.NewWithID(appID)
	a.Settings().SetTheme(fluaTheme{})

	iconOn := fyne.NewStaticResource("proxy-on.svg", iconOnSVG)
	iconOff := fyne.NewStaticResource("proxy-off.svg", iconOffSVG)
	a.SetIcon(iconOff)

	w := a.NewWindow(windowTitle)
	// Sized once, here. Resizing per screen made the window jump around as
	// the person navigated, and a window that changes shape when you look
	// at a different part of it reads as broken.
	w.Resize(fyne.NewSize(460, 620))
	w.SetFixedSize(false)

	if DryRun() {
		w.SetTitle(windowTitle + " — demonstração (nada é aplicado de verdade)")
	}

	ui := &mainUI{app: a, win: w, iconOn: iconOn, iconOff: iconOff}
	ui.build()

	// Closing the window hides it; the tray keeps the daemon reachable and
	// "Sair" is the only way out. Without a tray there would be no way back,
	// so the intercept is installed only alongside one.
	if desk, ok := a.(desktop.App); ok {
		ui.desk = desk
		ui.installTray()
		w.SetCloseIntercept(func() { w.Hide() })
		if hidden {
			w.Hide()
		}
	}

	ui.watchCredentials()

	w.ShowAndRun()
	return nil
}

// credentialCheckInterval is how often the window re-reads what the daemon
// observed.
//
// A minute, not a second: the state changes when a password expires, which
// happens once every few months, and polling faster would only spend wake-ups
// to learn nothing.
const credentialCheckInterval = time.Minute

// watchCredentials keeps the warning current while the window is open.
//
// Checking only at startup would miss the case this feature exists for: the
// password expires during the day, with the app already running.
func (u *mainUI) watchCredentials() {
	go func() {
		for range time.Tick(credentialCheckInterval) {
			fyne.Do(func() {
				if u.refreshWarning != nil {
					u.refreshWarning()
				}
			})
		}
	}()
}

type mainUI struct {
	app     fyne.App
	win     fyne.Window
	desk    desktop.App
	iconOn  fyne.Resource
	iconOff fyne.Resource

	on      bool
	profile string
	// repaintStatus redraws the status screen after the state changes from
	// the tray, which has no reference to the screen's widgets.
	repaintStatus func()
	// refreshWarning re-checks whether the daemon is being refused.
	refreshWarning func()
}

// showSetup draws the first-run form. It is reused to add a profile: the
// fields and the real probe are the same, so a second, nearly identical
// screen would only be a copy that drifts.
func (u *mainUI) showSetup(after func()) {
	u.showSetupFor("", SetupFields{}, after)
}

// showSetupFor draws the form, optionally pre-filled and saving under a
// given profile name.
func (u *mainUI) showSetupFor(profileName string, initial SetupFields, after func()) {
	screen := newSetupScreen(initial, func(name string, cfg proxy.Config, user, pass string) {
		// Renaming while editing means the old entry has to go, or the
		// person ends up with two profiles where they meant to have one.
		if profileName != "" && name != profileName {
			if _, err := RemoveProfile(profileName); err != nil {
				dialogError(u.win, err)
				return
			}
		}
		if err := SaveProfileNamed(name, cfg, user, pass); err != nil {
			dialogError(u.win, err)
			return
		}
		u.profile = name
		if after != nil {
			after()
			return
		}
		u.showStatus()
	})
	u.win.SetContent(container.NewPadded(screen.content))
}

func (u *mainUI) build() {
	pf, err := proxy.LoadProfiles()
	if err != nil || len(pf.Profiles) == 0 {
		// Nothing configured: the first-run form is the whole app until it
		// succeeds.
		u.showSetup(nil)
		return
	}
	u.profile = pf.ActiveProfile
	// Read the machine before drawing: the person may have changed the
	// Windows proxy settings by hand since the last run.
	if on, name, err := CurrentState(); err == nil {
		u.on, u.profile = on, name
	}
	u.showStatus()
}

func (u *mainUI) showStatus() {
	seal := container.NewStack()

	state := canvas.NewText("Desligado", colorMuted)
	state.TextSize = stateSize
	state.TextStyle = fyne.TextStyle{Bold: true}
	state.Alignment = fyne.TextAlignCenter

	detail := canvas.NewText("", colorExplain)
	detail.TextSize = explainSize
	detail.Alignment = fyne.TextAlignCenter

	toggle := widget.NewButton("", nil)

	paint := func() {
		seal.RemoveAll()
		if u.on {
			seal.Add(newStateSeal(true, u.iconOn))
		} else {
			seal.Add(newStateSeal(false, u.iconOff))
		}
		seal.Refresh()

		if u.on {
			state.Text = "Ligado"
			// Green for the positive state, never the brand colour.
			state.Color = colorSuccess
			setText(detail, "Sua internet está passando pelo proxy. Tudo funcionando.")
			toggle.SetText("Desligar o proxy")
			toggle.Importance = widget.MediumImportance
			u.setIcon(u.iconOn)
		} else {
			state.Text = "Desligado"
			state.Color = colorMuted
			setText(detail, "Sua internet está saindo direto, sem passar pelo proxy.")
			toggle.SetText("Ligar o proxy")
			toggle.Importance = widget.HighImportance
			u.setIcon(u.iconOff)
		}
		state.Refresh()
		toggle.Refresh()
	}
	toggle.OnTapped = func() { u.toggle(toggle, paint) }
	u.repaintStatus = paint

	profileLabel := canvas.NewText("Perfil", colorMuted)
	profileLabel.TextSize = cardLabel
	// Switching is the frequent action, so it happens here rather than
	// behind a screen. Managing profiles is rare and gets its own link:
	// one button doing both is what made "Trocar" the only way to reach
	// editing, which is not what the word says.
	names, active, _ := Profiles()
	swap := widget.NewSelect(names, func(chosen string) {
		if chosen == "" || chosen == u.profile {
			return
		}
		// Repaint the pieces that changed rather than rebuilding the
		// screen: SetContent flashes the window, and it rebuilt the picker
		// from disk, which in a dry run has not changed.
		u.switchProfile(chosen, func() {
			if u.repaintStatus != nil {
				u.repaintStatus()
			}
		})
	})
	swap.PlaceHolder = "nenhum perfil"
	swap.SetSelected(active)

	profileCard := container.NewBorder(nil, nil, container.NewCenter(profileLabel), nil, swap)

	diagnosticsLink := widget.NewButton("Ver diagnóstico", u.showDiagnostics)
	diagnosticsLink.Importance = widget.LowImportance

	// The daemon may have been refused since this screen was last drawn.
	warning := container.NewVBox()
	u.refreshWarning = func() {
		warning.RemoveAll()
		if problem, profile := UpstreamTrouble(); profile == u.profile {
			if msg, needsPassword, ok := TroubleMessage(problem); ok {
				warning.Add(newNoticeCard(msg))
				if needsPassword {
					warning.Add(widget.NewButton("Digitar a nova senha", func() {
						u.showSetup(func() { u.showStatus() })
					}))
				}
			}
		}
		warning.Refresh()
	}
	u.refreshWarning()

	// Layout of the canvas: brand strip, seal, state, explanation, action,
	// the profile card, and the diagnostics link pinned to the bottom.
	manageLink := widget.NewButton("Gerenciar perfis", u.showProfiles)
	manageLink.Importance = widget.LowImportance

	footer := container.NewHBox(manageLink, layout.NewSpacer(), diagnosticsLink)

	body := container.NewVBox(
		newBrandBar(u.iconOn),
		warning,
		vSpace(spaceAboveSeal),
		container.NewCenter(seal),
		vSpace(spaceSealToState),
		container.NewCenter(state),
		vSpace(spaceStateToText),
		container.NewCenter(detail),
		vSpace(spaceAboveAction),
		toggle,
		vSpace(spaceActionToCard),
		newCard(profileCard),
	)

	// The footer sits at the bottom, as designed — but the window is sized
	// to the content just above, so "at the bottom" does not mean "after a
	// hole".
	u.win.SetContent(container.NewPadded(container.NewBorder(nil, footer, nil, nil, body)))
	paint()
}

// toggle applies the change and only then repaints.
//
// Repainting first and applying afterwards would show "Ligado" for the
// moment it takes to write the settings, and would keep showing it if the
// write failed — the window would be reporting an intention rather than the
// state of the machine.
func (u *mainUI) toggle(button *widget.Button, paint func()) {
	button.Disable()
	want := !u.on

	go func() {
		var err error
		if want {
			_, err = TurnOn(u.profile)
		} else {
			_, err = TurnOff(u.profile)
		}

		fyne.Do(func() {
			button.Enable()
			if err != nil {
				dialogError(u.win, err)
				return
			}
			// u.profile is deliberately kept: turning the proxy off clears
			// the active profile on disk, and forgetting it here would
			// leave the person unable to turn it back on without going
			// through the profile screen again.
			u.on = want
			paint()
			u.refreshTray()
		})
	}()
}

func (u *mainUI) profileLabel() string {
	if u.profile == "" {
		return "nenhum"
	}
	return u.profile
}

func (u *mainUI) setIcon(res fyne.Resource) {
	u.app.SetIcon(res)
	if u.desk != nil {
		u.desk.SetSystemTrayIcon(res)
	}
}

func (u *mainUI) installTray() {
	u.refreshTray()
}

func (u *mainUI) refreshTray() {
	if u.desk == nil {
		return
	}
	label := "Ligar o proxy"
	if u.on {
		label = "Desligar o proxy"
	}
	menu := fyne.NewMenu(windowTitle,
		fyne.NewMenuItem(label, func() {
			want := !u.on
			go func() {
				var err error
				if want {
					_, err = TurnOn(u.profile)
				} else {
					_, err = TurnOff(u.profile)
				}
				fyne.Do(func() {
					if err != nil {
						dialogError(u.win, err)
						return
					}
					u.on = want
					if u.repaintStatus != nil {
						u.repaintStatus()
					}
					u.refreshTray()
				})
			}()
		}),
		fyne.NewMenuItemSeparator(),
		autostartItem(u),
		fyne.NewMenuItemSeparator(),
		// Recommended even where left-click opens the window: not every
		// desktop honours that.
		fyne.NewMenuItem("Mostrar janela", func() { u.win.Show() }),
		fyne.NewMenuItem("Sair", func() { u.app.Quit() }),
	)
	u.desk.SetSystemTrayMenu(menu)
}

// autostartItem is the "start with Windows" toggle.
//
// It lives in the tray rather than in a settings screen because the tray is
// where this app is used, and because someone who wants to stop it starting
// is most likely looking at the icon that just appeared.
func autostartItem(u *mainUI) *fyne.MenuItem {
	item := fyne.NewMenuItem("Iniciar com o Windows", nil)
	item.Checked = AutostartEnabled()
	item.Action = func() {
		var err error
		if item.Checked {
			err = RemoveAutostart(executor())
		} else {
			err = InstallAutostart(executor())
		}
		if err != nil {
			dialogError(u.win, err)
			return
		}
		u.refreshTray()
	}
	return item
}

// dialogError shows a failure the person can act on. Saving is the one
// place where a silent failure would leave them believing the setup worked.
func dialogError(w fyne.Window, err error) {
	dialog.ShowError(err, w)
}
