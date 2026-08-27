package wingui

import (
	"strings"
	"testing"

	"proxy-helper/internal/serve"
)

// healthy is a machine where everything works, so each test can change the
// one thing it is about.
func healthy() DiagnosticsInput {
	return DiagnosticsInput{
		ProfileName:   "Escritório",
		Upstream:      "proxy.interno:3128",
		DaemonRunning: true,
		SystemApplied: true,
		Autostart:     true,
		NoProxyCount:  4,
		HasProfile:    true,
	}
}

func findCheck(t *testing.T, checks []Check, title string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Title == title {
			return c
		}
	}
	t.Fatalf("no check titled %q in %+v", title, checks)
	return Check{}
}

func TestDiagnosticsSpeaksTheReadersLanguage(t *testing.T) {
	// The failure this guards against is real: the first implementation
	// listed "shell", "dockerd", "lxd" — this project's internal names, on a
	// screen built for someone who has never heard of a target.
	forbidden := []string{"wininet", "dockerd", "shell", "lxd", "snap", "target", "registry", "daemon", "systemd", "407", "HKCU"}

	for _, check := range Diagnostics(healthy()) {
		text := strings.ToLower(check.Title + " " + check.Detail)
		for _, word := range forbidden {
			if strings.Contains(text, strings.ToLower(word)) {
				t.Errorf("check %q mentions %q, which the reader cannot act on: %s", check.Title, word, text)
			}
		}
	}
}

func TestEveryCheckSaysSomething(t *testing.T) {
	checks := Diagnostics(healthy())
	if len(checks) == 0 {
		t.Fatal("a configured machine produced no checks")
	}
	for _, c := range checks {
		if c.Title == "" || c.Detail == "" || c.State == "" {
			t.Errorf("incomplete check: %+v", c)
		}
	}
}

func TestAHealthyMachineReportsNoProblem(t *testing.T) {
	for _, c := range Diagnostics(healthy()) {
		if c.State == CheckBad {
			t.Errorf("check %q reports a problem on a healthy machine: %s", c.Title, c.Detail)
		}
	}
}

func TestAnUnreachableProxyDoesNotBlameThePassword(t *testing.T) {
	in := healthy()
	in.UpstreamIssue = serve.ProblemUnreachable

	checks := Diagnostics(in)
	if got := findCheck(t, checks, "Conexão com o proxy"); got.State != CheckBad {
		t.Errorf("the connection check is %q, want a problem", got.State)
	}
	// The password may be perfectly good; nothing answered to check it.
	// Marking it bad would send someone to retype a working password.
	if got := findCheck(t, checks, "Sua senha"); got.State == CheckBad {
		t.Errorf("the password was blamed for the proxy being down: %s", got.Detail)
	}
}

func TestARejectedPasswordDoesNotBlameTheConnection(t *testing.T) {
	in := healthy()
	in.UpstreamIssue = serve.ProblemRejected

	checks := Diagnostics(in)
	if got := findCheck(t, checks, "Sua senha"); got.State != CheckBad {
		t.Errorf("the password check is %q, want a problem", got.State)
	}
	// The proxy answered — that is how we know it refused us.
	if got := findCheck(t, checks, "Conexão com o proxy"); got.State == CheckBad {
		t.Errorf("the connection was blamed although the proxy answered: %s", got.Detail)
	}
}

func TestAStoppedDaemonIsReportedAsAProblem(t *testing.T) {
	in := healthy()
	in.DaemonRunning = false

	// Without it the proxy cannot work at all, so this is never a detail.
	got := findCheck(t, Diagnostics(in), "Serviço em segundo plano")
	if got.State != CheckBad {
		t.Errorf("state = %q, want a problem", got.State)
	}
}

func TestTheProxyBeingOffIsNotAFailure(t *testing.T) {
	in := healthy()
	in.SystemApplied = false

	// Someone deliberately turned it off. Reporting that as broken would
	// teach people that this screen cries wolf.
	got := findCheck(t, Diagnostics(in), "Configuração do computador")
	if got.State == CheckBad {
		t.Errorf("a deliberately disabled proxy is reported as broken: %s", got.Detail)
	}
}

func TestAMachineWithNoProfileSaysSoAndStops(t *testing.T) {
	checks := Diagnostics(DiagnosticsInput{})

	// Listing "your password: fine" on a machine with nothing configured
	// would be answering questions nobody asked while hiding the only one
	// that matters.
	if len(checks) != 1 {
		t.Fatalf("got %d checks, want the single one about nothing being configured: %+v", len(checks), checks)
	}
	if checks[0].State != CheckBad {
		t.Errorf("state = %q, want a problem", checks[0].State)
	}
}

func TestTheReleasedSitesLineCountsCorrectly(t *testing.T) {
	cases := map[int]string{0: "nenhum", 1: "1 endereço", 5: "5 endereços"}
	for count, want := range cases {
		in := healthy()
		in.NoProxyCount = count
		got := findCheck(t, Diagnostics(in), "Sites liberados")
		if !strings.Contains(got.Detail, want) {
			t.Errorf("with %d released sites the detail is %q, want it to contain %q", count, got.Detail, want)
		}
	}
}
