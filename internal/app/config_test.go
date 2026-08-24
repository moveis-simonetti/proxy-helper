package app

import (
	"errors"
	"slices"
	"testing"

	"proxy-helper/internal/proxy"
)

// The reload is the point of this function. Without it the daemon keeps
// routing by the previous list while config.json says otherwise: the setting
// looks saved and nothing changes. That was a real bug, reported by a user
// running --via-local.
func TestSetGlobalNoProxyReloadsTheDaemon(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	got, err := SetGlobalNoProxy(d, &proxy.Executor{}, []string{"*.corp"})
	if err != nil {
		t.Fatalf("SetGlobalNoProxy: %v", err)
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1 — a saved list the daemon never re-reads has no effect", reloads)
	}
	if want := []string{"*.corp"}; !slices.Equal(got, want) {
		t.Errorf("effective = %v, want %v", got, want)
	}

	pf, err := proxy.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if !slices.Equal(pf.GlobalNoProxy, []string{"*.corp"}) {
		t.Errorf("global_no_proxy = %v, want [*.corp]", pf.GlobalNoProxy)
	}
}

// nil is the reset: EffectiveGlobalNoProxy falls back to the default only on
// nil, so this is what "Restaurar padrão" and "config reset-no-proxy" pass.
func TestSetGlobalNoProxyWithNilRestoresTheDefault(t *testing.T) {
	isolateConfig(t, `{"global_no_proxy":["*.corp"],"profiles":{}}`)

	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return nil }}
	got, err := SetGlobalNoProxy(d, &proxy.Executor{}, nil)
	if err != nil {
		t.Fatalf("SetGlobalNoProxy: %v", err)
	}
	if !slices.Equal(got, proxy.DefaultGlobalNoProxy) {
		t.Errorf("effective = %v, want the default %v", got, proxy.DefaultGlobalNoProxy)
	}
}

// A non-nil empty slice is NOT the same as nil: it means "no host bypasses
// the proxy". Collapsing the two would silently turn a deliberate
// "bypass nothing" into "bypass the defaults".
func TestSetGlobalNoProxyWithAnEmptySliceMeansBypassNothing(t *testing.T) {
	isolateConfig(t, `{"global_no_proxy":["*.corp"],"profiles":{}}`)

	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return nil }}
	got, err := SetGlobalNoProxy(d, &proxy.Executor{}, []string{})
	if err != nil {
		t.Fatalf("SetGlobalNoProxy: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("effective = %v, want an empty list, not the default", got)
	}
}

// A failed reload has to surface: the daemon is then routing by a list the
// config no longer names, which is the confusing half-state this function
// exists to prevent.
func TestSetGlobalNoProxySurfacesAFailedReload(t *testing.T) {
	isolateConfig(t, `{"profiles":{}}`)

	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return errors.New("boom") }}
	if _, err := SetGlobalNoProxy(d, &proxy.Executor{}, []string{"*.corp"}); err == nil {
		t.Fatal("SetGlobalNoProxy succeeded with a failing ReloadDaemon; want an error")
	}
}

// Editing the ACTIVE profile has to reach the daemon: with --via-local it
// resolves that profile's host, port, credentials and no-proxy itself, so a
// write nobody reloads leaves it proxying through the old values while
// config.json shows the new ones. This was a real bug, reported by a user
// whose edited no-proxy silently did nothing.
func TestSaveProfileReloadsWhenTheProfileIsActive(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"old.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	cfg := proxy.Config{Host: "new.example", NoProxy: []string{"*.corp"}}
	resolve := func(map[string]proxy.Config) (string, proxy.Config, error) { return "corp", cfg, nil }
	if err := SaveProfile(d, &proxy.Executor{}, resolve); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1 — an edited active profile the daemon never re-reads has no effect", reloads)
	}

	pf, _ := proxy.LoadProfiles()
	if got := pf.Profiles["corp"].Host; got != "new.example" {
		t.Errorf("host = %q, want %q", got, "new.example")
	}
}

// Editing some OTHER profile changes nothing the daemon is using, so a SIGHUP
// there would only log a reload the user cannot explain.
func TestSaveProfileDoesNotReloadForAnInactiveProfile(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"p.example"},"casa":{"host":"h.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	resolve := func(map[string]proxy.Config) (string, proxy.Config, error) {
		return "casa", proxy.Config{Host: "nova.example"}, nil
	}
	if err := SaveProfile(d, &proxy.Executor{}, resolve); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}
}

// A rejected write must leave the file untouched, and must not reload.
func TestSaveProfileRejectedByGuardWritesNothing(t *testing.T) {
	isolateConfig(t, `{"active_profile":"corp","profiles":{"corp":{"host":"p.example"}}}`)

	reloads := 0
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { reloads++; return nil }}

	sentinel := errors.New("já existe um perfil com esse nome")
	resolve := func(map[string]proxy.Config) (string, proxy.Config, error) {
		return "corp", proxy.Config{Host: "outro.example"}, sentinel
	}

	err := SaveProfile(d, &proxy.Executor{}, resolve)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the guard's own error unchanged", err)
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}

	pf, _ := proxy.LoadProfiles()
	if got := pf.Profiles["corp"].Host; got != "p.example" {
		t.Errorf("host = %q, want it untouched at %q", got, "p.example")
	}
}

// resolve sees what is on disk, inside the lock — that is what makes a
// duplicate-name check meaningful rather than advisory.
func TestSaveProfileGuardSeesTheProfilesOnDisk(t *testing.T) {
	isolateConfig(t, `{"profiles":{"corp":{"host":"p.example"}}}`)

	var seen []string
	d := Deps{ReloadDaemon: func(*proxy.Executor) error { return nil }}
	resolve := func(existing map[string]proxy.Config) (string, proxy.Config, error) {
		for name := range existing {
			seen = append(seen, name)
		}
		return "casa", proxy.Config{Host: "h.example"}, nil
	}
	if err := SaveProfile(d, &proxy.Executor{}, resolve); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if !slices.Contains(seen, "corp") {
		t.Errorf("resolve saw %v, want it to include the existing %q", seen, "corp")
	}
}
