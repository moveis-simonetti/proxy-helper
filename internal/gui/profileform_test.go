package gui

import (
	"slices"
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

func TestValidateProfileFormRejectsTheReservedName(t *testing.T) {
	_, _, msg := validateProfileForm(profileFormValues{Name: "_current", Host: "p.example"}, nil, "")
	if msg == "" {
		t.Fatal("aceitou o nome reservado \"_current\"")
	}
	if !strings.Contains(msg, "_current") {
		t.Errorf("mensagem não cita o nome recusado: %q", msg)
	}
}

func TestValidateProfileFormRequiresAHost(t *testing.T) {
	_, _, msg := validateProfileForm(profileFormValues{Name: "corp"}, nil, "")
	if msg == "" {
		t.Fatal("aceitou um perfil sem host")
	}
}

func TestValidateProfileFormRejectsADuplicateName(t *testing.T) {
	existing := map[string]proxy.Config{"corp": {Host: "p.example"}}
	_, _, msg := validateProfileForm(profileFormValues{Name: "corp", Host: "o.example"}, existing, "")
	if msg == "" {
		t.Fatal("aceitou um nome que já existe")
	}
}

// Editing a profile must not trip the duplicate check on its own name —
// otherwise no profile could ever be edited without renaming it.
func TestValidateProfileFormAllowsEditingUnderTheSameName(t *testing.T) {
	existing := map[string]proxy.Config{"corp": {Host: "p.example"}}
	_, _, msg := validateProfileForm(profileFormValues{Name: "corp", Host: "novo.example"}, existing, "corp")
	if msg != "" {
		t.Fatalf("recusou a edição do próprio perfil: %s", msg)
	}
}

func TestValidateProfileFormRejectsANonNumericPort(t *testing.T) {
	_, _, msg := validateProfileForm(profileFormValues{Name: "corp", Host: "p.example", Port: "oitenta"}, nil, "")
	if msg == "" {
		t.Fatal("aceitou uma porta não numérica")
	}
}

func TestValidateProfileFormRejectsAPortOutOfRange(t *testing.T) {
	for _, port := range []string{"0", "65536", "-1"} {
		if _, _, msg := validateProfileForm(profileFormValues{Name: "c", Host: "p.example", Port: port}, nil, ""); msg == "" {
			t.Errorf("aceitou a porta %q", port)
		}
	}
}

func TestValidateProfileFormRejectsAnUnknownScheme(t *testing.T) {
	_, _, msg := validateProfileForm(profileFormValues{Name: "c", Host: "p.example", Scheme: "ftp"}, nil, "")
	if msg == "" {
		t.Fatal("aceitou um esquema desconhecido")
	}
}

func TestValidateProfileFormSplitsAndTrimsNoProxy(t *testing.T) {
	_, cfg, msg := validateProfileForm(profileFormValues{
		Name: "c", Host: "p.example", NoProxy: " *.corp , 10.0.0.0/8 ,, localhost ",
	}, nil, "")
	if msg != "" {
		t.Fatalf("recusou uma lista válida: %s", msg)
	}
	want := []string{"*.corp", "10.0.0.0/8", "localhost"}
	if !slices.Equal(cfg.NoProxy, want) {
		t.Errorf("NoProxy = %v, want %v", cfg.NoProxy, want)
	}
}

func TestValidateProfileFormDefaultsTheScheme(t *testing.T) {
	_, cfg, msg := validateProfileForm(profileFormValues{Name: "c", Host: "p.example"}, nil, "")
	if msg != "" {
		t.Fatalf("recusou um perfil válido: %s", msg)
	}
	if cfg.Scheme != "http" {
		t.Errorf("Scheme = %q, want %q", cfg.Scheme, "http")
	}
}

func TestSplitNoProxyDropsEmptyEntries(t *testing.T) {
	got := splitNoProxy(" a , , b ,")
	want := []string{"a", "b"}
	if !slices.Equal(got, want) {
		t.Errorf("splitNoProxy = %v, want %v", got, want)
	}
}

// An empty box means "no global list", which is not the same as a list of
// one empty string — that would push "" down to every target.
// nil e slice vazia NÃO são equivalentes aqui: EffectiveGlobalNoProxy
// (internal/proxy/profiles.go:56) testa != nil, então uma slice vazia
// significa "nenhum host burla o proxy", enquanto nil significa "use o
// padrão". Guardar o nil é o que impede essa inversão silenciosa.
func TestSplitNoProxyOnAnEmptyStringReturnsNil(t *testing.T) {
	if got := splitNoProxy("   "); got != nil {
		t.Errorf("splitNoProxy = %#v, want nil", got)
	}
}

// The reserved slot is an implementation detail of "proxy set --via-local",
// not something the user saved: showing it would invite editing it.
func TestVisibleProfilesHidesTheReservedSlot(t *testing.T) {
	pf := &proxy.ProfileFile{Profiles: map[string]proxy.Config{
		"corp":                   {Host: "p.example"},
		proxy.CurrentProfileName: {Host: "127.0.0.1"},
		"casa":                   {Host: "h.example"},
	}}
	got := visibleProfiles(pf)
	want := []string{"casa", "corp"}
	if !slices.Equal(got, want) {
		t.Errorf("visibleProfiles = %v, want %v", got, want)
	}
}

// If this ever regresses to routeRewritesTargets, the headerbar would
// rewrite the 11 targets' own config and pop an unannounced pkexec password
// prompt on every profile switch, even though the daemon plumbing (and its
// no-touch guarantee) is already in place.
func TestRouteForSwitchWithViaLocalIsStateOnly(t *testing.T) {
	if got := routeForSwitch(true); got != routeStateOnly {
		t.Errorf("routeForSwitch(true) = %v, want routeStateOnly", got)
	}
}

// If this ever regresses to routeStateOnly, the headerbar would switch the
// active profile without ever asking the user, without rewriting any
// target, and without popping the pkexec prompt the rewrite actually
// needs — the profile would show as active while every target still points
// at the old upstream.
func TestRouteForSwitchWithoutViaLocalRewritesTargets(t *testing.T) {
	if got := routeForSwitch(false); got != routeRewritesTargets {
		t.Errorf("routeForSwitch(false) = %v, want routeRewritesTargets", got)
	}
}

func TestFormTitleSaysWhichModeItIsIn(t *testing.T) {
	if got := formTitle(modeNew, ""); got != "Novo perfil" {
		t.Errorf("formTitle(modeNew) = %q", got)
	}
	if got := formTitle(modeEdit, "corp"); got != "Editando “corp”" {
		t.Errorf("formTitle(modeEdit) = %q", got)
	}
}

func TestFormTitleQuotesTheProfileName(t *testing.T) {
	if got := formTitle(modeNew, ""); got != "Novo perfil" {
		t.Errorf("formTitle(modeNew) = %q", got)
	}
	if got := formTitle(modeEdit, "Casa"); got != "Editando “Casa”" {
		t.Errorf("formTitle(modeEdit) = %q, want %q", got, "Editando “Casa”")
	}
}

// The button names the operation so the mode indicator is not the only cue:
// a user who missed the heading still reads "Criar perfil" before clicking.
func TestPrimaryActionLabelNamesTheOperation(t *testing.T) {
	if got := primaryActionLabel(modeNew); got != "Criar perfil" {
		t.Errorf("primaryActionLabel(modeNew) = %q", got)
	}
	if got := primaryActionLabel(modeEdit); got != "Salvar alterações" {
		t.Errorf("primaryActionLabel(modeEdit) = %q", got)
	}
}

func TestCanCancelIsFalseWithNothingChanged(t *testing.T) {
	v := profileFormValues{Name: "Casa", Scheme: "http"}
	if canCancel(v, v) {
		t.Error("canCancel = true with nothing to undo")
	}
}

func TestCanCancelIsTrueOnceSomethingChanged(t *testing.T) {
	base := profileFormValues{Name: "Casa", Scheme: "http"}
	cur := base
	cur.Host = "1.1.1.1"
	if !canCancel(base, cur) {
		t.Error("canCancel = false though the host changed")
	}
}

// An untouched form is not dirty: filling the form from a profile must not
// by itself make it look edited, or every row click would ask to discard.
func TestIsDirtyIsFalseForAnUntouchedForm(t *testing.T) {
	v := profileFormValues{Name: "corp", Host: "p.example", Port: "3128"}
	if isDirty(v, v) {
		t.Error("isDirty = true for identical values")
	}
}

func TestIsDirtySpotsAChangeInEveryField(t *testing.T) {
	base := profileFormValues{
		Name: "corp", Scheme: "http", Host: "p.example",
		Port: "3128", User: "u", Pass: "s", NoProxy: "*.corp",
	}
	// One case per field: a struct gaining a field and not gaining a
	// comparison is exactly the silent bug this guards.
	mutations := map[string]func(*profileFormValues){
		"Name":    func(v *profileFormValues) { v.Name = "outro" },
		"Scheme":  func(v *profileFormValues) { v.Scheme = "socks5" },
		"Host":    func(v *profileFormValues) { v.Host = "outro.example" },
		"Port":    func(v *profileFormValues) { v.Port = "8080" },
		"User":    func(v *profileFormValues) { v.User = "outro" },
		"Pass":    func(v *profileFormValues) { v.Pass = "outra" },
		"NoProxy": func(v *profileFormValues) { v.NoProxy = "*.outro" },
	}
	for field, mutate := range mutations {
		cur := base
		mutate(&cur)
		if !isDirty(base, cur) {
			t.Errorf("isDirty missed a change in %s", field)
		}
	}
}

// canSave compares against the baseline the form was populated with, never
// against the zero struct. startNew selects a default scheme, so an untouched
// new form carries Scheme:"http" — treating the zero struct as "empty" made
// that form look savable, and (via isDirty) look like unsaved work worth
// warning about on the first row click. Both were reported by a user.
func TestCanSaveIsFalseForAnUntouchedNewFormCarryingItsDefaultScheme(t *testing.T) {
	blank := profileFormValues{Scheme: "http"}
	if canSave(blank, blank) {
		t.Error("canSave = true for an untouched new form that only carries its default scheme")
	}
}

func TestIsDirtyIsFalseForAnUntouchedNewFormCarryingItsDefaultScheme(t *testing.T) {
	blank := profileFormValues{Scheme: "http"}
	if isDirty(blank, blank) {
		t.Error("isDirty = true for an untouched new form; this is what pops the discard dialog")
	}
}

func TestCanSaveIsTrueOnceTheNewFormHasSomethingTyped(t *testing.T) {
	blank := profileFormValues{Scheme: "http"}
	cur := blank
	cur.Name = "corp"
	if !canSave(blank, cur) {
		t.Error("canSave = false though a name was typed")
	}
}

func TestCanSaveIsFalseForAnUnchangedEdit(t *testing.T) {
	v := profileFormValues{Name: "corp", Scheme: "http", Host: "p.example"}
	if canSave(v, v) {
		t.Error("canSave = true for an unchanged edit — it would rewrite the same bytes and reload the daemon for nothing")
	}
}

func TestCanSaveIsTrueForAChangedEdit(t *testing.T) {
	base := profileFormValues{Name: "corp", Scheme: "http", Host: "p.example"}
	cur := base
	cur.Host = "novo.example"
	if !canSave(base, cur) {
		t.Error("canSave = false though the host changed")
	}
}

// The blank selector next to an "Ativo" master switch was a real report: the
// reserved slot is hidden from the profiles list, so SetActiveID("_current")
// matched nothing and the combo settled on no row at all.
func TestHeaderbarExtraEntryNamesTheReservedSlot(t *testing.T) {
	id, label, ok := headerbarExtraEntry(proxy.CurrentProfileName)
	if !ok {
		t.Fatal("headerbarExtraEntry(_current) = not ok; the selector would show nothing")
	}
	if id != proxy.CurrentProfileName {
		t.Errorf("id = %q, want %q", id, proxy.CurrentProfileName)
	}
	if label != currentSlotLabel {
		t.Errorf("label = %q, want %q", label, currentSlotLabel)
	}
}

func TestHeaderbarExtraEntryIsThePlaceholderWithNothingActive(t *testing.T) {
	id, label, ok := headerbarExtraEntry("")
	if !ok || id != "" || label != "Nenhum perfil" {
		t.Errorf("headerbarExtraEntry(\"\") = %q, %q, %v", id, label, ok)
	}
}

// A saved profile is its own selection; an extra row beside it would be an
// inert option to pick.
func TestHeaderbarExtraEntryIsAbsentForASavedProfile(t *testing.T) {
	if _, _, ok := headerbarExtraEntry("Trabalho"); ok {
		t.Error("headerbarExtraEntry(\"Trabalho\") = ok; a saved profile needs no extra row")
	}
}
