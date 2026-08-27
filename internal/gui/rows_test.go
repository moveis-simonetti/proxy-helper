package gui

import (
	"strings"
	"testing"

	"proxy-helper/internal/proxy"
)

func TestRowForEnabled(t *testing.T) {
	row := rowFor(proxy.Status{Name: "git", Available: true, Enabled: true, Detail: "http.proxy set"}, false)

	if row.Label != "aplicado" {
		t.Errorf("Label = %q, want %q", row.Label, "aplicado")
	}
	if row.Marker != "●" {
		t.Errorf("Marker = %q, want %q", row.Marker, "●")
	}
	if row.Color != "#26a269" {
		t.Errorf("Color = %q, want %q", row.Color, "#26a269")
	}
	if !row.Selectable {
		t.Error("Selectable = false, want true")
	}
	if row.Root {
		t.Error("Root = true, want false")
	}
	if row.Detail != "http.proxy set" {
		t.Errorf("Detail = %q, want %q", row.Detail, "http.proxy set")
	}
}

func TestRowForAvailableNotEnabled(t *testing.T) {
	row := rowFor(proxy.Status{Name: "npm", Available: true, Enabled: false}, false)

	if row.Label != "sem proxy" {
		t.Errorf("Label = %q, want %q", row.Label, "sem proxy")
	}
	if row.Marker != "○" {
		t.Errorf("Marker = %q, want %q", row.Marker, "○")
	}
	if row.Color != "#8b8e8f" {
		t.Errorf("Color = %q, want %q", row.Color, "#8b8e8f")
	}
	if !row.Selectable {
		t.Error("Selectable = false, want true")
	}
}

func TestRowForNeedsElevation(t *testing.T) {
	row := rowFor(proxy.Status{Name: "snap", Available: true, NeedsElevation: true}, true)

	if row.Label != "requer sudo" {
		t.Errorf("Label = %q, want %q", row.Label, "requer sudo")
	}
	if row.Marker != "▲" {
		t.Errorf("Marker = %q, want %q", row.Marker, "▲")
	}
	if row.Color != "#c64600" {
		t.Errorf("Color = %q, want %q", row.Color, "#c64600")
	}
	if !row.Selectable {
		t.Error("Selectable = false, want true")
	}
	if !row.Root {
		t.Error("Root = false, want true")
	}
}

func TestRowForUnavailable(t *testing.T) {
	row := rowFor(proxy.Status{Name: "kde", Available: false, Detail: "kwriteconfig/plasmashell not found"}, false)

	if row.Label != "indisponível" {
		t.Errorf("Label = %q, want %q", row.Label, "indisponível")
	}
	if row.Marker != "—" {
		t.Errorf("Marker = %q, want %q", row.Marker, "—")
	}
	if row.Selectable {
		t.Error("Selectable = true, want false — an unavailable target must never be selectable")
	}
	if row.Detail != "kwriteconfig/plasmashell not found" {
		t.Errorf("Detail = %q, want %q", row.Detail, "kwriteconfig/plasmashell not found")
	}
}

// TestRowForUnavailableWinsOverOtherSignals covers a target reported both
// unavailable and (implausibly) enabled: unavailable must still win, since
// it is never an error and the row must never show as active.
func TestRowForUnavailableWinsOverOtherSignals(t *testing.T) {
	row := rowFor(proxy.Status{Name: "weird", Available: false, Enabled: true, NeedsElevation: true}, false)

	if row.Label != "indisponível" {
		t.Errorf("Label = %q, want %q", row.Label, "indisponível")
	}
	if row.Selectable {
		t.Error("Selectable = true, want false")
	}
}

// TestRowForMarkers pins the glyph-per-state mapping the Status table's
// "Situação" column relies on to be recognisable at a glance.
func TestRowForMarkers(t *testing.T) {
	cases := []struct {
		name string
		st   proxy.Status
		root bool
		want string
	}{
		{"aplicado", proxy.Status{Name: "git", Available: true, Enabled: true}, false, "●"},
		{"sem proxy", proxy.Status{Name: "git", Available: true}, false, "○"},
		{"requer sudo", proxy.Status{Name: "snap", Available: true, NeedsElevation: true}, false, "▲"},
		{"indisponível", proxy.Status{Name: "kde", Available: false}, false, "—"},
	}
	for _, c := range cases {
		got := rowFor(c.st, c.root)
		if got.Marker != c.want {
			t.Errorf("%s: Marker = %q, want %q", c.name, got.Marker, c.want)
		}
	}
}

func TestElevationBarTextSingular(t *testing.T) {
	got := elevationBarText(1)
	want := "1 alvo não pode ser lido sem sudo."
	if got != want {
		t.Errorf("elevationBarText(1) = %q, want %q", got, want)
	}
}

func TestElevationBarTextPlural(t *testing.T) {
	got := elevationBarText(2)
	want := "2 alvos não podem ser lidos sem sudo."
	if got != want {
		t.Errorf("elevationBarText(2) = %q, want %q", got, want)
	}
}

// "Requer sudo" and "indisponível" tint the whole row; the two ordinary
// states do not. A lone coloured word among eleven rows is easy to read past,
// and these are exactly the two the user has to notice.
func TestTintWholeRowOnlyForStatesTheUserMustNotice(t *testing.T) {
	cases := []struct {
		name string
		st   proxy.Status
		want bool
	}{
		{"indisponível", proxy.Status{Name: "kde", Available: false}, true},
		{"requer sudo", proxy.Status{Name: "snap", Available: true, NeedsElevation: true}, true},
		{"ativo", proxy.Status{Name: "git", Available: true, Enabled: true}, false},
		{"inativo", proxy.Status{Name: "git", Available: true}, false},
	}
	for _, c := range cases {
		got := rowFor(c.st, false)
		if got.TintWholeRow != c.want {
			t.Errorf("%s: TintWholeRow = %v, want %v", c.name, got.TintWholeRow, c.want)
		}
	}
}

// The elevation row stays selectable: needing sudo to READ it says nothing
// about whether the user may apply to it, and disabling the checkbox would
// silently drop it from the next apply.
func TestNeedsElevationRowStaysSelectable(t *testing.T) {
	got := rowFor(proxy.Status{Name: "snap", Available: true, NeedsElevation: true}, false)
	if !got.Selectable {
		t.Error("Selectable = false; a target that merely needs sudo to read must stay applicable")
	}
}

// A target that could not be read is neither applied nor unapplied. Folding it
// into either count would state something the program does not know — and
// "requer sudo" means exactly that the read failed.
func TestSummaryTextKeepsUnreadTargetsOutOfTheAppliedCount(t *testing.T) {
	got := summaryText(9, 11, 7, 1, 0)
	for _, want := range []string{"9 de 11 alvos selecionados", "7 já aplicados", "1 não verificados"} {
		if !strings.Contains(got, want) {
			t.Errorf("summaryText = %q, faltou %q", got, want)
		}
	}
	if plain := summaryText(11, 11, 0, 0, 0); plain != "11 de 11 alvos selecionados" {
		t.Errorf("sem aplicados nem desconhecidos deveria ficar só a contagem, veio %q", plain)
	}
}

// gnome is the case that produced the report: RequiresRoot is true wherever
// the PackageKit cache exists, but it is session-scoped and so never routed
// through pkexec — it costs no password, and the footer counted it anyway
// while the padlock column did not.
func TestPromptsForPasswordExcludesSessionScopedTargets(t *testing.T) {
	if promptsForPassword(true, true) {
		t.Error("promptsForPassword(root, sessionScoped) = true; that target never reaches pkexec")
	}
	if !promptsForPassword(true, false) {
		t.Error("promptsForPassword(root, not scoped) = false; apt/snap/dockerd do prompt")
	}
	if promptsForPassword(false, false) {
		t.Error("promptsForPassword(no root, not scoped) = true")
	}
}

// The password warning used to be a sentence of its own, in a strip of its
// own, sharing the button row with the last run's summary — two competing
// texts that wrapped and pushed the buttons onto a second line. It is a
// property of the selection like the other three counts, so it belongs in
// the same sentence.
func TestSummaryTextCountsWhatWillAskForAPassword(t *testing.T) {
	if got := summaryText(9, 11, 8, 1, 3); !strings.Contains(got, "3 pedem senha") {
		t.Errorf("summaryText = %q, faltou a contagem de cadeados", got)
	}
	if got := summaryText(9, 11, 8, 1, 1); !strings.Contains(got, "1 pede senha") {
		t.Errorf("singular errado: %q", got)
	}
	// Nothing selected costs a password: saying so would be noise on every
	// unprivileged selection.
	if got := summaryText(9, 11, 8, 1, 0); strings.Contains(got, "senha") {
		t.Errorf("summaryText = %q, não deveria falar de senha com zero cadeados", got)
	}
}
