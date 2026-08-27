package wingui

import (
	"strings"
	"testing"

	"proxy-helper/internal/serve"
)

func TestTrimmedCleansPastedFields(t *testing.T) {
	// Pasting from a chat message carries whitespace, and a proxy rejects
	// "gestao " exactly as it rejects a wrong password.
	got := SetupFields{Name: " Escritório ", Address: "  proxy.interno:3128 ", Username: " gestao\t"}.Trimmed()
	if got.Address != "proxy.interno:3128" {
		t.Errorf("Address = %q, want %q", got.Address, "proxy.interno:3128")
	}
	if got.Username != "gestao" {
		t.Errorf("Username = %q, want %q", got.Username, "gestao")
	}
	if got.Name != "Escritório" {
		t.Errorf("Name = %q, want %q", got.Name, "Escritório")
	}
}

func TestTrimmedKeepsThePasswordExactly(t *testing.T) {
	// A trailing space can be part of a password. Dropping it would break a
	// login for a reason nobody could ever find by looking at the screen.
	const senha = " senha com espaco "
	if got := (SetupFields{Password: senha}).Trimmed(); got.Password != senha {
		t.Errorf("Password = %q, want it untouched (%q)", got.Password, senha)
	}
}

func TestCompleteRequiresEveryField(t *testing.T) {
	full := SetupFields{Name: "Escritório", Address: "proxy.interno:3128", Username: "gestao", Password: "senha"}
	if !full.Complete() {
		t.Error("a fully filled form reported incomplete")
	}

	for name, f := range map[string]SetupFields{
		// The name is required like the rest: a profile nobody named is a
		// row in a list nobody can read.
		"no name":     {Address: "proxy.interno:3128", Username: "gestao", Password: "senha"},
		"no address":  {Name: "Escritório", Username: "gestao", Password: "senha"},
		"no username": {Name: "Escritório", Address: "proxy.interno:3128", Password: "senha"},
		"no password": {Name: "Escritório", Address: "proxy.interno:3128", Username: "gestao"},
		"only spaces": {Name: " ", Address: "  ", Username: " ", Password: "senha"},
	} {
		t.Run(name, func(t *testing.T) {
			if f.Complete() {
				t.Error("reported complete")
			}
		})
	}
}

func TestPhaseForMapsEveryProbeOutcome(t *testing.T) {
	cases := map[serve.ProbeResult]SetupPhase{
		serve.ProbeOK:             PhaseOK,
		serve.ProbeBadCredentials: PhaseRefused,
		serve.ProbeUnknownHost:    PhaseUnknownHost,
		serve.ProbeUnreachable:    PhaseUnreachable,
	}
	for result, want := range cases {
		if got := PhaseFor(result); got != want {
			t.Errorf("PhaseFor(%v) = %v, want %v", result, got, want)
		}
	}
}

func TestMessagesAvoidTechnicalVocabulary(t *testing.T) {
	// The audience cannot act on a status code or a service name, and a
	// message they cannot act on becomes a support ticket.
	forbidden := []string{"407", "HTTP", "systemd", "daemon", "registry", "WinINET", "DPAPI", "error", "null"}

	for _, phase := range []SetupPhase{PhaseIncomplete, PhaseOK, PhaseUnknownHost, PhaseRefused, PhaseUnreachable} {
		msg, ok := MessageFor(phase)
		if !ok {
			t.Errorf("phase %v has no message", phase)
			continue
		}
		if msg.Title == "" || msg.Body == "" {
			t.Errorf("phase %v has an empty message: %+v", phase, msg)
		}
		text := msg.Title + " " + msg.Body
		for _, word := range forbidden {
			if strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
				t.Errorf("phase %v mentions %q, which the reader cannot act on: %s", phase, word, text)
			}
		}
	}
}

func TestQuietPhasesShowNoMessage(t *testing.T) {
	for _, phase := range []SetupPhase{PhaseIdle, PhaseTesting} {
		if msg, ok := MessageFor(phase); ok {
			t.Errorf("phase %v shows a message it should not: %+v", phase, msg)
		}
	}
}

func TestOnlyFailuresAreMarkedBad(t *testing.T) {
	if msg, _ := MessageFor(PhaseOK); msg.Bad {
		t.Error("the success message is marked as a failure")
	}
	for _, phase := range []SetupPhase{PhaseIncomplete, PhaseUnknownHost, PhaseRefused, PhaseUnreachable} {
		if msg, _ := MessageFor(phase); !msg.Bad {
			t.Errorf("phase %v is not marked as a failure", phase)
		}
	}
}

func TestFieldsAtFaultMarksTheCredentialsTogetherOnARefusal(t *testing.T) {
	// The proxy does not say which of the two was wrong, so highlighting
	// only one would be a guess presented as fact.
	address, username, password := FieldsAtFault(PhaseRefused)
	if address {
		t.Error("a refusal marked the address, which the proxy answered from")
	}
	if !username || !password {
		t.Error("a refusal must mark the username and the password together")
	}
}

func TestFieldsAtFaultMarksOnlyTheAddressOnALookupFailure(t *testing.T) {
	address, username, password := FieldsAtFault(PhaseUnknownHost)
	if !address {
		t.Error("an unresolvable address was not marked")
	}
	if username || password {
		t.Error("credentials were marked for a failure that never reached the proxy")
	}
}

func TestNothingIsMarkedWhileIdleOrSucceeding(t *testing.T) {
	for _, phase := range []SetupPhase{PhaseIdle, PhaseTesting, PhaseOK} {
		if a, u, p := FieldsAtFault(phase); a || u || p {
			t.Errorf("phase %v marks fields as wrong", phase)
		}
	}
}

func TestButtonSaysWhatIsHappening(t *testing.T) {
	if got := ButtonLabel(PhaseTesting); got == ButtonLabel(PhaseIdle) {
		t.Errorf("the button reads the same while idle and while testing (%q); the person cannot tell it is working", got)
	}
}
