//go:build wingui

package wingui

import (
	_ "embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
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
	w.Resize(fyne.NewSize(460, 560))

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

	w.ShowAndRun()
	return nil
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
}

// showSetup draws the first-run form. It is reused to add a profile: the
// fields and the real probe are the same, so a second, nearly identical
// screen would only be a copy that drifts.
func (u *mainUI) showSetup(after func()) {
	screen := newSetupScreen(func(cfg proxy.Config, user, pass string) {
		if err := SaveFirstProfile(cfg, user, pass); err != nil {
			dialogError(u.win, err)
			return
		}
		u.profile = defaultProfileName
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
	state := canvas.NewText("Desligado", colorMuted)
	state.TextSize = 30
	state.TextStyle = fyne.TextStyle{Bold: true}
	state.Alignment = fyne.TextAlignCenter

	detail := widget.NewLabel("")
	detail.Alignment = fyne.TextAlignCenter
	detail.Wrapping = fyne.TextWrapWord

	toggle := widget.NewButton("", nil)

	paint := func() {
		if u.on {
			state.Text = "Ligado"
			// Green for the positive state, never the brand colour.
			state.Color = colorSuccess
			detail.SetText("Sua internet está passando pelo proxy. Tudo funcionando.")
			toggle.SetText("Desligar o proxy")
			toggle.Importance = widget.MediumImportance
			u.setIcon(u.iconOn)
		} else {
			state.Text = "Desligado"
			state.Color = colorMuted
			detail.SetText("Sua internet está saindo direto, sem passar pelo proxy.")
			toggle.SetText("Ligar o proxy")
			toggle.Importance = widget.HighImportance
			u.setIcon(u.iconOff)
		}
		state.Refresh()
		toggle.Refresh()
	}
	toggle.OnTapped = func() { u.toggle(toggle, paint) }
	u.repaintStatus = paint

	profileRow := container.NewBorder(nil, nil,
		widget.NewLabel("Perfil"), nil,
		widget.NewLabel(u.profileLabel()),
	)

	links := container.NewHBox(
		widget.NewButton("Perfis", u.showProfiles),
		widget.NewButton("Diagnóstico", u.showDiagnostics),
	)
	for _, o := range links.Objects {
		if b, ok := o.(*widget.Button); ok {
			b.Importance = widget.LowImportance
		}
	}

	u.win.SetContent(container.NewPadded(container.NewVBox(
		state, detail,
		widget.NewSeparator(),
		toggle,
		profileRow,
		widget.NewSeparator(),
		links,
	)))
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
