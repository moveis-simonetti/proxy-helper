// Package wingui implements the Windows interface: a small, tray-first app
// for people who only need the browser to work.
//
// This file has no build tag and imports no toolkit on purpose. What it
// holds is the wording and the state machine of the first-run screen — the
// screen that decides whether the rollout generates a support queue — and
// that deserves tests, which a toolkit-bound file would not get.
package wingui

import (
	"strings"

	"proxy-helper/internal/serve"
)

// SetupPhase is where the first-run screen currently stands.
type SetupPhase int

const (
	// PhaseIdle: waiting for the person to fill the form.
	PhaseIdle SetupPhase = iota
	// PhaseIncomplete: they pressed the button with something missing.
	PhaseIncomplete
	// PhaseTesting: the probe is running.
	PhaseTesting
	// PhaseOK: the proxy answered and accepted the credentials.
	PhaseOK
	// PhaseUnknownHost: the address does not resolve.
	PhaseUnknownHost
	// PhaseRefused: the proxy refused the username or the password.
	PhaseRefused
	// PhaseUnreachable: the address resolves but nothing answered.
	PhaseUnreachable
)

// SetupFields is what the person typed.
type SetupFields struct {
	// Name is what this profile is called. It is the person's own word for
	// "the office" or "home" — the app never invents it, because a list of
	// profiles nobody named is a list nobody can read.
	Name     string
	Address  string
	Username string
	Password string
}

// Trimmed returns the fields with surrounding whitespace removed.
//
// Pasting an address or a username from a chat message routinely carries a
// trailing space, and a proxy rejects "gestao " as confidently as it rejects
// a wrong password — leaving the person to re-type something that already
// looked right.
func (f SetupFields) Trimmed() SetupFields {
	return SetupFields{
		Name:     strings.TrimSpace(f.Name),
		Address:  strings.TrimSpace(f.Address),
		Username: strings.TrimSpace(f.Username),
		// The password is deliberately NOT trimmed: a trailing space can be
		// part of it, and silently dropping one would break a login for a
		// reason nobody could ever find.
		Password: f.Password,
	}
}

// Complete reports whether all three fields carry something.
func (f SetupFields) Complete() bool {
	t := f.Trimmed()
	return t.Name != "" && t.Address != "" && t.Username != "" && t.Password != ""
}

// PhaseFor maps a probe outcome to the phase the screen should show.
func PhaseFor(result serve.ProbeResult) SetupPhase {
	switch result {
	case serve.ProbeOK:
		return PhaseOK
	case serve.ProbeBadCredentials:
		return PhaseRefused
	case serve.ProbeUnknownHost:
		return PhaseUnknownHost
	default:
		return PhaseUnreachable
	}
}

// Message is what the screen tells the person about the current phase.
type Message struct {
	Title string
	Body  string
	// Bad marks a phase that should read as a failure.
	Bad bool
}

// MessageFor returns the wording for a phase, or ok=false for the phases
// that show no message at all.
//
// No status codes, no service names, no "407": the audience cannot act on
// those, and a message they cannot act on becomes a support ticket.
func MessageFor(phase SetupPhase) (Message, bool) {
	switch phase {
	case PhaseIncomplete:
		return Message{
			Title: "Faltou preencher",
			Body:  "Preencha o nome, o endereço, o usuário e a senha.",
			Bad:   true,
		}, true
	case PhaseOK:
		return Message{
			Title: "Funcionou",
			Body:  "O proxy respondeu e o acesso foi aceito. Abrindo o MS Proxy…",
		}, true
	case PhaseUnknownHost:
		return Message{
			Title: "Endereço não encontrado",
			Body:  "Não existe nenhum proxy nesse endereço. Confira se está escrito certo.",
			Bad:   true,
		}, true
	case PhaseRefused:
		// The proxy does not say which of the two was wrong, so neither do
		// we. Pointing at the username is still useful: it has no standard
		// format here, so it is the likelier mistake.
		return Message{
			Title: "Usuário ou senha recusados",
			Body:  "O proxy não aceitou esses dados. Confira o usuário: o formato varia de um proxy para outro.",
			Bad:   true,
		}, true
	case PhaseUnreachable:
		return Message{
			Title: "Proxy não respondeu",
			Body:  "Não conseguimos falar com o proxy agora. Tente de novo em instantes.",
			Bad:   true,
		}, true
	}
	return Message{}, false
}

// ButtonLabel is what the action button says in each phase.
func ButtonLabel(phase SetupPhase) string {
	switch phase {
	case PhaseTesting:
		return "Testando o acesso…"
	case PhaseOK:
		return "Tudo certo"
	default:
		return "Testar e continuar"
	}
}

// FieldsAtFault says which inputs to mark as wrong for a phase.
//
// A refusal marks the username and the password together because the proxy
// does not distinguish them; an address failure marks only the address.
// Marking everything on every failure would tell the person nothing.
func FieldsAtFault(phase SetupPhase) (address, username, password bool) {
	switch phase {
	case PhaseIncomplete:
		return true, true, true
	case PhaseRefused:
		return false, true, true
	case PhaseUnknownHost:
		return true, false, false
	}
	return false, false, false
}
