package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Off/On used to move the name between ActiveProfile and LastProfile.
// Routing now lives in Mode, so the selection survives the round trip
// untouched — see TestOffKeepsProfileSelected in mode_test.go for why that
// matters. LastProfile is still mirrored for a pre-Mode daemon.
func TestOffRemembersAndOnRestores(t *testing.T) {
	pf := &ProfileFile{
		ActiveProfile: "work",
		Profiles:      map[string]Config{"work": {Host: "proxy.corp"}},
	}

	pf.Off()
	if pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want it still selected after Off", pf.ActiveProfile)
	}
	if pf.EffectiveMode() != ModeDirect {
		t.Errorf("mode = %q, want %q after Off", pf.EffectiveMode(), ModeDirect)
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
	if pf.EffectiveMode() == ModeDirect {
		t.Error("mode is still direct after On")
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
	if pf.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want it still selected", pf.ActiveProfile)
	}
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

// TestWithProfileLockSerializesWriters is the regression test for a lost
// update: two concurrent read-modify-write cycles must both survive.
func TestWithProfileLockSerializesWriters(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := WithProfileLock(func(pf *ProfileFile) error {
				if pf.Profiles == nil {
					pf.Profiles = map[string]Config{}
				}
				pf.Profiles[fmt.Sprintf("p%d", i)] = Config{Host: "h", Port: "1"}
				return nil
			})
			if err != nil {
				t.Errorf("WithProfileLock: %v", err)
			}
		}(i)
	}
	wg.Wait()

	pf, err := LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(pf.Profiles) != n {
		t.Errorf("expected all %d writes to survive, got %d: %v", n, len(pf.Profiles), pf.Profiles)
	}
}

// TestSaveIsAtomic checks the write-temp-then-rename property that keeps a
// concurrent reader from ever observing a torn file: after Save returns, the
// target file parses cleanly, is 0600, and no ".config.json.tmp-*" scratch
// file is left behind in the config directory.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	pf := &ProfileFile{
		ActiveProfile: "work",
		Profiles:      map[string]Config{"work": {Host: "proxy.corp", Port: "8080"}},
	}
	if err := pf.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path, err := ConfigFilePath()
	if err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("config file mode = %v, want 0600", mode)
	}

	loaded, err := LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles after Save: %v", err)
	}
	if loaded.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", loaded.ActiveProfile)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reading config dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file in config dir: %s", e.Name())
		}
	}
}
