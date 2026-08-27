package gui

import (
	"testing"

	"proxy-helper/internal/proxy"
)

func TestValidateImportFormEmptyURL(t *testing.T) {
	_, _, msg := validateImportForm(importFormValues{Name: "corp"}, proxy.PACProxy{Scheme: "http", Host: "h", Port: "8080"}, nil)
	if msg != "Informe a URL do arquivo PAC." {
		t.Errorf("msg = %q", msg)
	}
}

func TestValidateImportFormEmptyName(t *testing.T) {
	v := importFormValues{URL: "http://x/p.pac"}
	_, _, msg := validateImportForm(v, proxy.PACProxy{Scheme: "http", Host: "h", Port: "8080"}, nil)
	if msg != "O nome do perfil é obrigatório." {
		t.Errorf("msg = %q", msg)
	}
}

func TestValidateImportFormReservedName(t *testing.T) {
	v := importFormValues{URL: "http://x/p.pac", Name: proxy.CurrentProfileName}
	_, _, msg := validateImportForm(v, proxy.PACProxy{Scheme: "http", Host: "h", Port: "8080"}, nil)
	want := `"_current" é reservado para "proxy set --via-local". Escolha outro nome.`
	if msg != want {
		t.Errorf("msg = %q, want %q", msg, want)
	}
}

func TestValidateImportFormDuplicateName(t *testing.T) {
	existing := map[string]proxy.Config{"corp": {}}
	v := importFormValues{URL: "http://x/p.pac", Name: "corp"}
	_, _, msg := validateImportForm(v, proxy.PACProxy{Scheme: "http", Host: "h", Port: "8080"}, existing)
	want := `Já existe um perfil chamado "corp".`
	if msg != want {
		t.Errorf("msg = %q, want %q", msg, want)
	}
}

func TestValidateImportFormNoEntryChosen(t *testing.T) {
	v := importFormValues{URL: "http://x/p.pac", Name: "corp"}
	_, _, msg := validateImportForm(v, proxy.PACProxy{}, nil)
	if msg != "Escolha um dos proxies encontrados." {
		t.Errorf("msg = %q", msg)
	}
}

// The PAC gives scheme/host/port; the credentials come from the form. A
// corporate proxy almost always needs both halves, and losing either one
// produces a profile that cannot authenticate.
func TestValidateImportFormCombinesThePacEntryWithTheTypedCredentials(t *testing.T) {
	chosen := proxy.PACProxy{Scheme: "http", Host: "10.0.0.5", Port: "8080"}
	v := importFormValues{URL: "http://x/p.pac", Name: "corp", User: "u", Pass: "s3cr3t"}
	name, cfg, msg := validateImportForm(v, chosen, nil)
	if msg != "" {
		t.Fatalf("recusou um formulário válido: %s", msg)
	}
	if name != "corp" {
		t.Errorf("name = %q", name)
	}
	if cfg.Host != "10.0.0.5" || cfg.Port != "8080" || cfg.Scheme != "http" {
		t.Errorf("cfg não veio da entrada do PAC: %+v", cfg)
	}
	if cfg.Username != "u" || cfg.Password != "s3cr3t" {
		t.Errorf("cfg não veio das credenciais digitadas: user=%q", cfg.Username)
	}
}

// Warning only where there is a choice to get wrong.
func TestPacWarningOnlyAppearsWithSomethingToChooseBetween(t *testing.T) {
	if pacWarning(0) != "" || pacWarning(1) != "" {
		t.Error("avisou sem haver ambiguidade")
	}
	if pacWarning(2) == "" {
		t.Error("não avisou com duas entradas, onde a escolha pode estar errada")
	}
}

func TestPacEntryLabel(t *testing.T) {
	p := proxy.PACProxy{Scheme: "http", Host: "10.0.0.5", Port: "8080"}
	if got := pacEntryLabel(p); got != "http://10.0.0.5:8080" {
		t.Errorf("pacEntryLabel = %q", got)
	}
}

// ParsePAC is pure (no network); feed it real PAC content and confirm the
// resulting entries format the way the Importar page expects.
func TestValidateImportFormWithRealPacContent(t *testing.T) {
	pac := `function FindProxyForURL(url, host) {
		if (shExpMatch(host, "*.internal")) {
			return "DIRECT";
		}
		return "PROXY 10.0.0.5:8080; PROXY 10.0.0.6:8080";
	}`
	entries, err := proxy.ParsePAC(pac)
	if err != nil {
		t.Fatalf("ParsePAC: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	if warn := pacWarning(len(entries)); warn == "" {
		t.Error("esperava aviso com duas entradas do PAC")
	}
	if got := pacEntryLabel(entries[0]); got != "http://10.0.0.5:8080" {
		t.Errorf("pacEntryLabel(entries[0]) = %q", got)
	}

	v := importFormValues{URL: "http://x/p.pac", Name: "corp", User: "u", Pass: "s3cr3t"}
	name, cfg, msg := validateImportForm(v, entries[0], nil)
	if msg != "" {
		t.Fatalf("recusou um formulário válido: %s", msg)
	}
	if name != "corp" || cfg.Host != "10.0.0.5" || cfg.Port != "8080" || cfg.Scheme != "http" {
		t.Errorf("cfg = %+v, name = %q", cfg, name)
	}
}
