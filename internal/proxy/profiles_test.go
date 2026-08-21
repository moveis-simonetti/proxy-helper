package proxy

import "testing"

func TestOffRemembersAndOnRestores(t *testing.T) {
	pf := &ProfileFile{
		ActiveProfile: "work",
		Profiles:      map[string]Config{"work": {Host: "proxy.corp"}},
	}

	pf.Off()
	if pf.ActiveProfile != "" {
		t.Errorf("ActiveProfile = %q, want empty after Off", pf.ActiveProfile)
	}
	if pf.LastProfile != "work" {
		t.Errorf("LastProfile = %q, want work", pf.LastProfile)
	}

	if err := pf.On(""); err != nil {
		t.Fatalf("On(\"\"): %v", err)
	}
	if pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work after On", pf.ActiveProfile)
	}
}

func TestOnWithoutLastProfileFails(t *testing.T) {
	pf := &ProfileFile{Profiles: map[string]Config{}}
	err := pf.On("")
	if err == nil {
		t.Fatal("On(\"\") with no last profile must fail")
	}
}

func TestOnWithUnknownProfileFails(t *testing.T) {
	pf := &ProfileFile{Profiles: map[string]Config{}}
	if err := pf.On("ghost"); err == nil {
		t.Fatal("On with an undefined profile must fail")
	}
}

func TestOffTwiceKeepsFirstLastProfile(t *testing.T) {
	pf := &ProfileFile{
		ActiveProfile: "work",
		Profiles:      map[string]Config{"work": {Host: "proxy.corp"}},
	}
	pf.Off()
	pf.Off() // already off; must not clobber LastProfile with ""
	if pf.LastProfile != "work" {
		t.Errorf("LastProfile = %q, want work", pf.LastProfile)
	}
}

func TestSetCurrentStoresReservedProfile(t *testing.T) {
	pf := &ProfileFile{Profiles: map[string]Config{}}
	pf.SetCurrent(Config{Host: "proxy.corp", Port: "8080"})

	if pf.ActiveProfile != CurrentProfileName {
		t.Errorf("ActiveProfile = %q, want %q", pf.ActiveProfile, CurrentProfileName)
	}
	cfg, ok := pf.Get(CurrentProfileName)
	if !ok || cfg.Host != "proxy.corp" {
		t.Errorf("reserved profile not stored, got %+v ok=%v", cfg, ok)
	}
}
