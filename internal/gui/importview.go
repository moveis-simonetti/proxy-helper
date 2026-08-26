// importview.go has no "gui" build tag and imports no GTK/glib, on purpose:
// the same reasoning as jobs.go, profileform.go and daemonview.go. It holds
// the Importar page's form validation — the whole of its judgement about
// what the user typed and picked — kept out of the GTK file so
// `go test ./...` exercises it without needing a graphical session.
package gui

import (
	"proxy-helper/internal/proxy"
)

// importFormValues is what the page's fields hold, exactly as typed.
type importFormValues struct {
	URL, Name, User, Pass string
}

// validateImportForm turns what the user typed into the profile to save, or
// returns the Portuguese message explaining what is wrong. chosen is the PAC
// entry the user picked; existing is the profiles already on disk.
//
// A zero proxy.PACProxy{} means "nothing chosen" — the design deliberately
// treats it as the not-picked-yet sentinel rather than a real (if unlikely)
// PAC entry, so it produces the "choose one" message instead of being saved.
func validateImportForm(v importFormValues, chosen proxy.PACProxy, existing map[string]proxy.Config) (name string, cfg proxy.Config, errMsg string) {
	if v.URL == "" {
		return "", proxy.Config{}, "Informe a URL do arquivo PAC."
	}

	name, errMsg = validateProfileName(v.Name, existing, "")
	if errMsg != "" {
		return "", proxy.Config{}, errMsg
	}

	if chosen == (proxy.PACProxy{}) {
		return "", proxy.Config{}, "Escolha um dos proxies encontrados."
	}

	cfg = proxy.Config{
		Scheme:   chosen.Scheme,
		Host:     chosen.Host,
		Port:     chosen.Port,
		Username: v.User,
		Password: v.Pass,
	}
	return name, cfg, ""
}

// pacWarning is the caveat shown under the results. It is empty for a single
// entry — with nothing to choose between there is no ambiguity to warn about,
// and a permanent warning would just be noise on the common case.
func pacWarning(entryCount int) string {
	if entryCount < 2 {
		return ""
	}
	return "A lista vem das diretivas literais do arquivo. O JavaScript do PAC não é avaliado, então pode haver entradas que só valem para certos destinos."
}

// pacEntryLabel is how one entry is listed for choosing.
func pacEntryLabel(p proxy.PACProxy) string {
	return p.String()
}
