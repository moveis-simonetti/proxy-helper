package wingui

import (
	"fmt"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// defaultProfileName is what the first profile is called. The person never
// asked for a profile — they asked for the proxy to work — so the first-run
// screen does not make them name one.
const defaultProfileName = "Padrão"

// SaveFirstProfile stores the configuration the probe just accepted and
// makes it active.
//
// It goes through WithProfileLock like every other writer: the daemon may be
// running and reading the same file, and a read-modify-write that skipped
// the lock could lose an update.
func SaveFirstProfile(cfg proxy.Config, user, pass string) error {
	return SaveProfileNamed("", cfg, user, pass)
}

// SaveProfileNamed stores a profile under name, or under the default name
// when name is empty, and makes it active.
func SaveProfileNamed(name string, cfg proxy.Config, user, pass string) error {
	if name == "" {
		name = defaultProfileName
	}
	cfg.Username = user
	if err := serve.StorePassword(&cfg, pass); err != nil {
		return err
	}

	return proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		if pf.Profiles == nil {
			pf.Profiles = map[string]proxy.Config{}
		}
		pf.Profiles[name] = cfg
		pf.ActiveProfile = name
		return nil
	})
}

// RemoveProfile deletes a saved profile.
//
// Removing the one in use is allowed but is not silent: the caller is told,
// so it can turn the proxy off rather than leave the machine pointing at a
// profile that no longer exists. The alternative — refusing — would strand
// someone whose only profile is the broken one they need to replace.
func RemoveProfile(name string) (wasActive bool, err error) {
	if name == "" {
		return false, fmt.Errorf("nenhum perfil informado")
	}
	err = proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		if _, ok := pf.Profiles[name]; !ok {
			return fmt.Errorf("o perfil %q não existe", name)
		}
		wasActive = pf.ActiveProfile == name
		delete(pf.Profiles, name)
		if wasActive {
			// Leaving a dangling active name would make every later read
			// report a profile that cannot be loaded.
			pf.ActiveProfile = ""
		}
		return nil
	})
	return wasActive, err
}
