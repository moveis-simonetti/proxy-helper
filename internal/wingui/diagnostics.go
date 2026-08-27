package wingui

import (
	"fmt"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// CheckState is how a diagnostic line reads.
type CheckState string

const (
	// CheckOK: working.
	CheckOK CheckState = "OK"
	// CheckNeutral: a fact, not a verdict — a count, a setting.
	CheckNeutral CheckState = "Normal"
	// CheckBad: broken, and the reason someone opened this screen.
	CheckBad CheckState = "Problema"
)

// Check is one line of the diagnostics list.
type Check struct {
	Title  string
	Detail string
	State  CheckState
}

// DiagnosticsInput is everything the checks are derived from. Taking it as
// data rather than reading the machine here is what makes the wording
// testable: the sentences below are the product, and they have to hold for
// every combination, including the ones nobody can reproduce on demand.
type DiagnosticsInput struct {
	ProfileName   string
	Upstream      string
	UpstreamIssue serve.UpstreamProblem
	DaemonRunning bool
	SystemApplied bool
	SystemDetail  string
	Autostart     bool
	NoProxyCount  int
	HasProfile    bool
}

// Diagnostics turns the machine's state into lines a person can read.
//
// Deliberately not a list of targets: "shell", "dockerd", "wininet" are
// this project's internal names, and the audience for this screen cannot
// act on any of them. Each line answers a question someone would actually
// ask — is it reaching the proxy, is my password good, is the browser using
// it — because the alternative is a screen they photograph and send to
// support without understanding.
func Diagnostics(in DiagnosticsInput) []Check {
	if !in.HasProfile {
		return []Check{{
			Title:  "Nenhum proxy configurado",
			Detail: "Este computador ainda não recebeu um endereço de proxy.",
			State:  CheckBad,
		}}
	}

	checks := []Check{connectionCheck(in), passwordCheck(in), systemCheck(in), backgroundCheck(in)}

	detail := "nenhum endereço liberado"
	switch in.NoProxyCount {
	case 1:
		detail = "1 endereço não passa pelo proxy"
	default:
		if in.NoProxyCount > 1 {
			detail = fmt.Sprintf("%d endereços não passam pelo proxy", in.NoProxyCount)
		}
	}
	checks = append(checks, Check{Title: "Sites liberados", Detail: detail, State: CheckNeutral})

	return checks
}

func connectionCheck(in DiagnosticsInput) Check {
	c := Check{Title: "Conexão com o proxy", Detail: in.Upstream, State: CheckOK}
	if in.Upstream == "" {
		c.Detail = "sem endereço"
	}
	if in.UpstreamIssue == serve.ProblemUnreachable {
		c.Detail = "o proxy não está respondendo"
		c.State = CheckBad
	}
	return c
}

func passwordCheck(in DiagnosticsInput) Check {
	// A rejection is the only thing that proves a password wrong. Everything
	// else — including not having tried yet — is "as far as we know, fine",
	// and claiming otherwise would send someone to retype a working one.
	if in.UpstreamIssue == serve.ProblemRejected {
		return Check{
			Title:  "Sua senha",
			Detail: "o proxy parou de aceitar a senha guardada",
			State:  CheckBad,
		}
	}
	return Check{Title: "Sua senha", Detail: "aceita e guardada neste computador", State: CheckOK}
}

func systemCheck(in DiagnosticsInput) Check {
	if !in.SystemApplied {
		return Check{
			Title:  "Configuração do computador",
			Detail: "o proxy está desligado",
			State:  CheckNeutral,
		}
	}
	detail := in.SystemDetail
	if detail == "" {
		detail = "navegador e demais programas estão usando o proxy"
	}
	return Check{Title: "Configuração do computador", Detail: detail, State: CheckOK}
}

func backgroundCheck(in DiagnosticsInput) Check {
	if !in.DaemonRunning {
		return Check{
			Title:  "Serviço em segundo plano",
			Detail: "não está rodando — o proxy não vai funcionar",
			State:  CheckBad,
		}
	}
	detail := "rodando"
	if in.Autostart {
		detail = "rodando, e inicia junto com o computador"
	}
	return Check{Title: "Serviço em segundo plano", Detail: detail, State: CheckOK}
}

// CollectDiagnostics reads the machine and builds the input.
func CollectDiagnostics() DiagnosticsInput {
	in := DiagnosticsInput{DaemonRunning: serve.DaemonActive(), Autostart: AutostartEnabled()}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		return in
	}
	in.ProfileName = pf.ActiveProfile
	in.NoProxyCount = len(pf.GlobalNoProxy)

	cfg, ok := pf.Profiles[pf.ActiveProfile]
	if !ok {
		return in
	}
	in.HasProfile = true
	in.NoProxyCount += len(cfg.NoProxy)
	if cfg.PACURL != "" {
		in.Upstream = "configuração automática"
	} else if cfg.Host != "" {
		in.Upstream = cfg.Host + ":" + cfg.Port
	}

	problem, profile := UpstreamTrouble()
	if profile == pf.ActiveProfile {
		in.UpstreamIssue = problem
	}

	if statuses, err := app.Collect(deps(), executor(), []string{"all"}, false); err == nil {
		for _, st := range statuses {
			if st.Available && st.Enabled {
				in.SystemApplied = true
				in.SystemDetail = st.Detail
				break
			}
		}
	}
	return in
}
