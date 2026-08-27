//go:build wingui

package wingui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"proxy-helper/internal/proxy"
)

// Sizes lifted from the canvas rather than rounded to a grid: the design is
// the specification, and a "close enough" 12 where it says 11.5 is how an
// implementation starts drifting from its design.
const (
	brandSize    = 13
	explainSize  = 13.5
	cardLabel    = 11.5
	cardValue    = 13.5
	sectionTitle = 15
	rowTitle     = 13
	rowDetail    = 11.5
)

var (
	cardFill   = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	cardStroke = color.NRGBA{R: 0xe7, G: 0xe7, B: 0xe7, A: 0xff}
)

// newBrandBar is the strip at the top of the main screen: the shield and the
// product's name, quiet and grey.
func newBrandBar(icon fyne.Resource) fyne.CanvasObject {
	image := canvas.NewImageFromResource(icon)
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(22, 22))

	name := canvas.NewText(windowTitle, colorMuted)
	name.TextSize = brandSize
	name.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewHBox(container.New(&fixedSize{width: 22, height: 22}, image), name)
}

// newBackBar is the header of a secondary screen: an arrow and a title.
func newBackBar(title string, back func()) fyne.CanvasObject {
	button := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), back)
	button.Importance = widget.LowImportance

	label := canvas.NewText(title, colorForeground)
	label.TextSize = sectionTitle
	label.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewHBox(button, label)
}

// newCard is the white, thin-bordered, 8px-radius panel the design uses for
// anything grouped.
func newCard(content fyne.CanvasObject) fyne.CanvasObject {
	panel := canvas.NewRectangle(cardFill)
	panel.StrokeColor = cardStroke
	panel.StrokeWidth = 1
	panel.CornerRadius = 8
	return container.NewStack(panel, newInset(content))
}

// newHairline is the 1px divider between rows inside a card.
func newHairline() fyne.CanvasObject {
	line := canvas.NewRectangle(cardStroke)
	line.SetMinSize(fyne.NewSize(0, 1))
	return line
}

// showProfiles lists the saved profiles.
func (u *mainUI) showProfiles() {
	names, active, err := Profiles()
	if err != nil {
		dialogError(u.win, err)
		return
	}

	rows := container.NewVBox()
	if len(names) == 0 {
		rows.Add(widget.NewLabel("Nenhum perfil salvo ainda."))
	}
	for i, name := range names {
		if i > 0 {
			rows.Add(newHairline())
		}
		rows.Add(u.profileRow(name, name == active))
	}

	add := widget.NewButtonWithIcon("Adicionar perfil", theme.ContentAddIcon(), func() {
		u.showSetup(func() { u.showStatus() })
	})

	u.win.SetContent(container.NewPadded(container.NewVBox(
		newBackBar("Perfis", u.showStatus),
		newCard(rows),
		add,
	)))
}

func (u *mainUI) profileRow(name string, active bool) fyne.CanvasObject {
	label := canvas.NewText(name, colorForeground)
	label.TextSize = cardValue
	if active {
		label.TextStyle = fyne.TextStyle{Bold: true}
	}

	edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() { u.editProfile(name) })
	edit.Importance = widget.LowImportance

	remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { u.confirmRemoveProfile(name) })
	remove.Importance = widget.LowImportance

	var trailing fyne.CanvasObject = widget.NewButton("Usar", func() { u.switchProfile(name, u.showProfiles) })
	if active {
		// Green, not the brand colour: "in use" is a healthy state, and the
		// brand is red.
		mark := canvas.NewText("em uso", colorSuccess)
		mark.TextSize = cardLabel
		mark.TextStyle = fyne.TextStyle{Bold: true}
		trailing = container.NewCenter(mark)
	}

	return container.NewBorder(nil, nil, label, container.NewHBox(trailing, edit, remove))
}

// confirmRemoveProfile asks before deleting.
//
// Deleting a profile throws away an address and a username someone had to
// obtain from somebody else, and there is no undo — so this is one of the
// few places in the app that stops to ask.
func (u *mainUI) confirmRemoveProfile(name string) {
	dialog.ShowConfirm(
		"Remover perfil",
		fmt.Sprintf("Remover o perfil %q? Isso não pode ser desfeito.", name),
		func(confirmed bool) {
			if !confirmed {
				return
			}
			wasActive, err := RemoveProfile(name)
			if err != nil {
				dialogError(u.win, err)
				return
			}
			if wasActive {
				// The machine may still be pointing at what was just
				// deleted; leaving it there would route traffic to a
				// profile that no longer exists.
				go func() {
					_, _ = TurnOff(name)
					fyne.Do(func() {
						u.on, u.profile = false, ""
						u.refreshTray()
						u.showProfiles()
					})
				}()
				return
			}
			u.showProfiles()
		},
		u.win,
	)
}

// switchProfile applies another profile and then redraws whatever screen
// the person was on.
//
// It takes that screen as an argument because it is reached from two
// places: the selector on the main screen and the list under Gerenciar
// perfis. Hard-coding the profile list — as this did at first — threw
// someone who only wanted to switch onto a screen they had not asked for.
//
// It goes through the same TurnOn as the main button: switching while the
// proxy is on has to rewrite the targets, or the machine keeps pointing at
// the old profile's address.
func (u *mainUI) switchProfile(name string, after func()) {
	go func() {
		_, err := TurnOn(name)
		fyne.Do(func() {
			if err != nil {
				dialogError(u.win, err)
				return
			}
			u.profile, u.on = name, true
			u.refreshTray()
			if after != nil {
				after()
			}
		})
	}()
}

// editProfile reopens the form filled with what the profile holds. Without
// it a mistyped address would be permanent.
func (u *mainUI) editProfile(name string) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		dialogError(u.win, err)
		return
	}
	cfg, ok := pf.Profiles[name]
	if !ok {
		dialogError(u.win, fmt.Errorf("o perfil %q não existe mais", name))
		return
	}

	address := cfg.PACURL
	if address == "" && cfg.Host != "" {
		address = cfg.Host + ":" + cfg.Port
	}
	u.showSetupFor(name, SetupFields{Name: name, Address: address, Username: cfg.Username}, u.showProfiles)
}

// showDiagnostics reports what the machine looks like, in the words of
// someone who does not know what a target is.
func (u *mainUI) showDiagnostics() {
	intro := canvas.NewText("Para quando alguém precisa saber o que está acontecendo.", colorMuted)
	intro.TextSize = 12.5

	rows := container.NewVBox()
	for i, check := range Diagnostics(CollectDiagnostics()) {
		if i > 0 {
			rows.Add(newHairline())
		}
		rows.Add(newCheckRow(check))
	}

	u.win.SetContent(container.NewPadded(container.NewVBox(
		newBackBar("Diagnóstico", u.showStatus),
		intro,
		newCard(rows),
	)))
}

// newCheckRow is one diagnostics line: a coloured dot, the question, the
// answer, and the verdict.
func newCheckRow(check Check) fyne.CanvasObject {
	tint := colorSuccess
	switch check.State {
	case CheckBad:
		tint = colorError
	case CheckNeutral:
		tint = colorMuted
	}

	dot := canvas.NewCircle(tint)
	dotBox := container.New(&fixedSize{width: 8, height: 8}, dot)

	title := canvas.NewText(check.Title, colorForeground)
	title.TextSize = rowTitle
	title.TextStyle = fyne.TextStyle{Bold: true}

	detail := canvas.NewText(check.Detail, colorMuted)
	detail.TextSize = rowDetail

	state := canvas.NewText(string(check.State), tint)
	state.TextSize = rowDetail
	state.TextStyle = fyne.TextStyle{Bold: true}

	return container.NewBorder(nil, nil,
		container.NewCenter(dotBox), container.NewCenter(state),
		container.NewVBox(title, detail),
	)
}
