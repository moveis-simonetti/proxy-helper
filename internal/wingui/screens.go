//go:build wingui

package wingui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
)

// backIcon and plusIcon come from Fyne's own set rather than the project's
// SVGs: these are navigation chrome, not identity, and a hand-drawn arrow
// would only be one more thing to keep consistent.
func backIcon() fyne.Resource { return theme.NavigateBackIcon() }
func plusIcon() fyne.Resource { return theme.ContentAddIcon() }

// newHeader builds the back-arrow-plus-title strip the secondary screens
// share.
func newHeader(title string, back func()) fyne.CanvasObject {
	button := widget.NewButtonWithIcon("", backIcon(), back)
	button.Importance = widget.LowImportance

	label := widget.NewLabel(title)
	label.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewBorder(nil, nil, button, nil, label)
}

// showProfiles lists the saved profiles and lets the person pick one.
//
// This screen exists because no profile is shipped and people switch
// between several: the first run creates one, and the others need somewhere
// to be born.
func (u *mainUI) showProfiles() {
	names, active, err := Profiles()
	if err != nil {
		dialogError(u.win, err)
		return
	}

	list := container.NewVBox()
	if len(names) == 0 {
		list.Add(widget.NewLabel("Nenhum perfil salvo ainda."))
	}
	for _, name := range names {
		list.Add(u.profileRow(name, name == active))
	}

	add := widget.NewButtonWithIcon("Adicionar perfil", plusIcon(), func() {
		u.showSetup(func() { u.showProfiles() })
	})

	u.win.SetContent(container.NewPadded(container.NewVBox(
		newHeader("Perfis", u.showStatus),
		widget.NewSeparator(),
		list,
		add,
	)))
}

// profileRow is one profile: its name, and whether it is the one in use.
func (u *mainUI) profileRow(name string, active bool) fyne.CanvasObject {
	label := widget.NewLabel(name)
	if active {
		label.TextStyle = fyne.TextStyle{Bold: true}
	}

	var trailing fyne.CanvasObject
	if active {
		// Green, not the brand colour: "in use" is a healthy state, and the
		// brand is red.
		mark := widget.NewLabel("em uso")
		mark.Importance = widget.SuccessImportance
		trailing = mark
	} else {
		trailing = widget.NewButton("Usar", func() { u.switchProfile(name) })
	}

	return container.NewBorder(nil, nil, nil, trailing, label)
}

// switchProfile applies another profile.
//
// It goes through the same TurnOn as the main button: switching profile
// while the proxy is on has to rewrite the targets, and a version that only
// changed the active name would leave the machine pointing at the old one.
func (u *mainUI) switchProfile(name string) {
	go func() {
		_, err := TurnOn(name)
		fyne.Do(func() {
			if err != nil {
				dialogError(u.win, err)
				return
			}
			u.profile = name
			u.on = true
			u.refreshTray()
			u.showProfiles()
		})
	}()
}

// showDiagnostics reports what the machine actually looks like, for when
// someone needs to know why something is not working.
//
// It reads every target rather than echoing the config: the whole point is
// to catch a disagreement between what was asked for and what is in place.
func (u *mainUI) showDiagnostics() {
	rows := container.NewVBox()

	statuses, err := app.Collect(deps(), executor(), []string{"all"}, false)
	switch {
	case err != nil:
		rows.Add(widget.NewLabel("Não foi possível ler a configuração deste computador."))
	case len(statuses) == 0:
		rows.Add(widget.NewLabel("Nada configurado neste computador."))
	default:
		for _, st := range statuses {
			rows.Add(diagnosticRow(st))
		}
	}

	u.win.SetContent(container.NewPadded(container.NewVBox(
		newHeader("Diagnóstico", u.showStatus),
		widget.NewLabel("Para quando alguém precisa saber o que está acontecendo."),
		widget.NewSeparator(),
		rows,
	)))
}

func diagnosticRow(st proxy.Status) fyne.CanvasObject {
	state := widget.NewLabel("desligado")
	switch {
	case !st.Available:
		state.SetText("não disponível")
	case st.Enabled:
		state.SetText("ligado")
		state.Importance = widget.SuccessImportance
	}

	name := widget.NewLabel(st.Name)
	detail := widget.NewLabel(st.Detail)
	detail.Wrapping = fyne.TextWrapWord

	return container.NewBorder(nil, nil, name, state, detail)
}
