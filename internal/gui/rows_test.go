package gui

import (
	"testing"

	"proxy-helper/internal/proxy"
)

func TestRowForEnabled(t *testing.T) {
	row := rowFor(proxy.Status{Name: "git", Available: true, Enabled: true, Detail: "http.proxy set"}, false)

	if row.Label != "ativo" {
		t.Errorf("Label = %q, want %q", row.Label, "ativo")
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

	if row.Label != "inativo" {
		t.Errorf("Label = %q, want %q", row.Label, "inativo")
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
