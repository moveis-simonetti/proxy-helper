//go:build wingui

package wingui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// setupScreen is the first-run form: address, username, password, and a
// button that actually tries them against the proxy.
//
// The probe is the point. Saving the credentials and letting the person
// discover later that browsing is broken puts the failure far from its
// cause — and since everyone is set up on the same day, "later" means a
// support queue.
type setupScreen struct {
	fields   SetupFields
	phase    SetupPhase
	onDone   func(name string, cfg proxy.Config, user, pass string)
	cancel   context.CancelFunc
	nameIn   *widget.Entry
	addrIn   *widget.Entry
	userIn   *widget.Entry
	passIn   *widget.Entry
	action   *widget.Button
	save     *widget.Button
	feedback *fyne.Container
	content  *fyne.Container
}

// newSetupScreen builds the form. initial pre-fills it, which is what makes
// this screen serve editing as well as first-run: the fields and the real
// probe are identical, and a second near-copy of them would only drift.
//
// The password is never pre-filled, even when editing. It is stored
// encrypted and cannot be read back for display — and showing a row of dots
// that stands for a value we cannot verify would be a worse lie than an
// empty field.
func newSetupScreen(initial SetupFields, onDone func(name string, cfg proxy.Config, user, pass string)) *setupScreen {
	s := &setupScreen{phase: PhaseIdle, onDone: onDone}
	s.fields = SetupFields{Name: initial.Name, Address: initial.Address, Username: initial.Username}

	title := canvas.NewText("Configurar o MS Proxy", colorForeground)
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}

	intro := widget.NewLabel("Informe o endereço do proxy e seu acesso.\nÉ só desta vez — depois fica tudo guardado.")
	intro.Wrapping = fyne.TextWrapWord

	s.nameIn = widget.NewEntry()
	s.nameIn.SetText(s.fields.Name)
	s.nameIn.SetPlaceHolder("Escritório, Casa…")
	s.nameIn.OnChanged = func(v string) { s.fields.Name = v; s.setPhase(PhaseIdle) }

	s.addrIn = widget.NewEntry()
	s.addrIn.SetText(s.fields.Address)
	s.addrIn.SetPlaceHolder("proxy.interno:3128 ou o endereço de um arquivo .pac")
	s.addrIn.OnChanged = func(v string) { s.fields.Address = v; s.setPhase(PhaseIdle) }

	s.userIn = widget.NewEntry()
	s.userIn.SetText(s.fields.Username)
	s.userIn.SetPlaceHolder("usuário de acesso ao proxy")
	s.userIn.OnChanged = func(v string) { s.fields.Username = v; s.setPhase(PhaseIdle) }

	s.passIn = widget.NewPasswordEntry()
	s.passIn.SetPlaceHolder("a senha que acompanha esse usuário")
	s.passIn.OnChanged = func(v string) { s.fields.Password = v; s.setPhase(PhaseIdle) }

	// Two buttons, because they answer different questions: "is this
	// right?" and "keep it". Merging them meant a person could not check an
	// address without committing it, and could not keep a profile the probe
	// happened to fail on — a proxy that is momentarily down is not a
	// reason to lose what was typed.
	s.action = widget.NewButton(ButtonLabel(PhaseIdle), s.submit)
	s.save = widget.NewButton("Salvar", s.commit)
	s.save.Importance = widget.HighImportance

	s.feedback = container.NewVBox()

	// A plain VBox, not a custom layout: Fyne already sizes a label above
	// its field correctly, and the hand-rolled one overlapped them.
	form := container.NewVBox(
		widget.NewLabel("Nome do perfil"), s.nameIn,
		widget.NewLabel("Endereço do proxy"), s.addrIn,
		widget.NewLabel("Usuário do proxy"), s.userIn,
		widget.NewLabel("Senha"), s.passIn,
	)

	footer := widget.NewLabel("Esses dados ficam guardados só neste computador.")
	footer.TextStyle = fyne.TextStyle{Italic: true}

	s.content = container.NewVBox(
		title, intro,
		widget.NewSeparator(),
		form,
		container.NewPadded(container.NewGridWithColumns(2, s.action, s.save)),
		s.feedback,
		widget.NewSeparator(),
		footer,
	)
	return s
}

func (s *setupScreen) submit() {
	if s.phase == PhaseTesting {
		return
	}
	fields := s.fields.Trimmed()
	if !fields.Complete() {
		s.setPhase(PhaseIncomplete)
		return
	}

	cfg, err := ConfigFromAddress(fields.Address)
	if err != nil {
		s.setPhase(PhaseUnknownHost)
		return
	}
	cfg.Username = fields.Username

	s.setPhase(PhaseTesting)

	// The probe runs off the UI thread; every widget touch afterwards goes
	// back through fyne.Do, because Fyne's widgets are not safe to mutate
	// from another goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.cancel = cancel
	go func() {
		defer cancel()
		result, _ := serve.Probe(ctx, cfg, fields.Username, fields.Password)
		phase := PhaseFor(result)
		fyne.Do(func() { s.setPhase(phase) })
	}()
}

// commit saves without testing.
//
// Saving an untested profile is allowed on purpose: the probe can fail for
// reasons that have nothing to do with what was typed, and refusing to save
// would throw away an address and a username the person may have had to ask
// someone for.
func (s *setupScreen) commit() {
	fields := s.fields.Trimmed()
	if !fields.Complete() {
		s.setPhase(PhaseIncomplete)
		return
	}
	cfg, err := ConfigFromAddress(fields.Address)
	if err != nil {
		s.setPhase(PhaseUnknownHost)
		return
	}
	cfg.Username = fields.Username
	if s.onDone != nil {
		s.onDone(fields.Name, cfg, fields.Username, fields.Password)
	}
}

func (s *setupScreen) setPhase(phase SetupPhase) {
	if s.phase == phase {
		return
	}
	s.phase = phase

	s.action.SetText(ButtonLabel(phase))
	if phase == PhaseTesting {
		s.action.Disable()
		s.save.Disable()
	} else {
		s.action.Enable()
		s.save.Enable()
	}

	s.feedback.RemoveAll()
	if msg, ok := MessageFor(phase); ok {
		s.feedback.Add(newNoticeCard(msg))
	}
	s.feedback.Refresh()
}
