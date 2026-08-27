package wingui

import (
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
	cfg.Username = user
	if err := serve.StorePassword(&cfg, pass); err != nil {
		return err
	}

	return proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
		if pf.Profiles == nil {
			pf.Profiles = map[string]proxy.Config{}
		}
		pf.Profiles[defaultProfileName] = cfg
		pf.ActiveProfile = defaultProfileName
		return nil
	})
}
