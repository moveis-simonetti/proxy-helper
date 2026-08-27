package wingui

import (
	"testing"

	"proxy-helper/internal/proxy"
)

// withTempConfig points the profile file at a directory the test owns.
func withTempConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
}

func TestRemovingTheActiveProfileClearsIt(t *testing.T) {
	withTempConfig(t)

	cfg := proxy.Config{Scheme: "http", Host: "proxy.interno", Port: "3128"}
	if err := SaveProfileNamed("Escritório", cfg, "gestao", "senha"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	wasActive, err := RemoveProfile("Escritório")
	if err != nil {
		t.Fatalf("removing: %v", err)
	}
	if !wasActive {
		t.Error("wasActive = false; the caller would not know to turn the proxy off")
	}

	// A dangling active name makes every later read report a profile that
	// cannot be loaded.
	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("reloading: %v", err)
	}
	if pf.ActiveProfile != "" {
		t.Errorf("ActiveProfile = %q, want it cleared", pf.ActiveProfile)
	}
	if _, still := pf.Profiles["Escritório"]; still {
		t.Error("the profile is still saved")
	}
}

func TestRemovingAnotherProfileLeavesTheActiveOneAlone(t *testing.T) {
	withTempConfig(t)

	cfg := proxy.Config{Scheme: "http", Host: "proxy.interno", Port: "3128"}
	if err := SaveProfileNamed("Casa", cfg, "gestao", "senha"); err != nil {
		t.Fatalf("saving Casa: %v", err)
	}
	if err := SaveProfileNamed("Escritório", cfg, "gestao", "senha"); err != nil {
		t.Fatalf("saving Escritório: %v", err)
	}

	wasActive, err := RemoveProfile("Casa")
	if err != nil {
		t.Fatalf("removing: %v", err)
	}
	if wasActive {
		t.Error("wasActive = true for a profile that was not in use; the proxy would be turned off for nothing")
	}

	pf, _ := proxy.LoadProfiles()
	if pf.ActiveProfile != "Escritório" {
		t.Errorf("ActiveProfile = %q, want %q", pf.ActiveProfile, "Escritório")
	}
}

func TestRemovingWhatIsNotThereFails(t *testing.T) {
	withTempConfig(t)

	if _, err := RemoveProfile("Inexistente"); err == nil {
		t.Error("expected an error for a profile that does not exist")
	}
	if _, err := RemoveProfile(""); err == nil {
		t.Error("expected an error for an empty name")
	}
}
