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
	onDone   func(proxy.Config, string, string)
	cancel   context.CancelFunc
	addrIn   *widget.Entry
	userIn   *widget.Entry
	passIn   *widget.Entry
	action   *widget.Button
	feedback *fyne.Container
	content  *fyne.Container
}

func newSetupScreen(onDone func(cfg proxy.Config, user, pass string)) *setupScreen {
	s := &setupScreen{phase: PhaseIdle, onDone: onDone}

	title := canvas.NewText("Configurar o MS Proxy", colorForeground)
	title.TextSize = 24
	title.TextStyle = fyne.TextStyle{Bold: true}

	intro := widget.NewLabel("Informe o endereço do proxy e seu acesso.\nÉ só desta vez — depois fica tudo guardado.")
	intro.Wrapping = fyne.TextWrapWord

	s.addrIn = widget.NewEntry()
	s.addrIn.SetPlaceHolder("proxy.interno:3128 ou o endereço de um arquivo .pac")
	s.addrIn.OnChanged = func(v string) { s.fields.Address = v; s.setPhase(PhaseIdle) }

	s.userIn = widget.NewEntry()
	s.userIn.SetPlaceHolder("usuário de acesso ao proxy")
	s.userIn.OnChanged = func(v string) { s.fields.Username = v; s.setPhase(PhaseIdle) }

	s.passIn = widget.NewPasswordEntry()
	s.passIn.SetPlaceHolder("a senha que acompanha esse usuário")
	s.passIn.OnChanged = func(v string) { s.fields.Password = v; s.setPhase(PhaseIdle) }

	s.action = widget.NewButton(ButtonLabel(PhaseIdle), s.submit)
	s.action.Importance = widget.HighImportance

	s.feedback = container.NewVBox()

	// A plain VBox, not a custom layout: Fyne already sizes a label above
	// its field correctly, and the hand-rolled one overlapped them.
	form := container.NewVBox(
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
		container.NewPadded(s.action),
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
		fyne.Do(func() {
			s.setPhase(phase)
			if phase == PhaseOK && s.onDone != nil {
				s.onDone(cfg, fields.Username, fields.Password)
			}
		})
	}()
}

func (s *setupScreen) setPhase(phase SetupPhase) {
	if s.phase == phase {
		return
	}
	s.phase = phase

	s.action.SetText(ButtonLabel(phase))
	if phase == PhaseTesting {
		s.action.Disable()
	} else {
		s.action.Enable()
	}

	s.feedback.RemoveAll()
	if msg, ok := MessageFor(phase); ok {
		s.feedback.Add(newNoticeCard(msg))
	}
	s.feedback.Refresh()
}
