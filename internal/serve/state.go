package serve

import (
	"fmt"
	"log/slog"
	"sync/atomic"

	"proxy-helper/internal/proxy"
)

// State holds everything a reload can swap. Requests read it through an
// atomic pointer, so switching profiles never blocks or drops connections:
// in-flight requests finish against the old state, new ones see the new one.
type State struct {
	current atomic.Pointer[snapshot]
	logger  *slog.Logger
}

type snapshot struct {
	router  Router
	profile string
	summary string
}

// NewState loads the current configuration. It fails only if the config is
// unreadable at startup; a broken config during Reload is non-fatal.
func NewState(logger *slog.Logger) (*State, error) {
	s := &State{logger: logger}
	snap, err := loadSnapshot(logger)
	if err != nil {
		return nil, err
	}
	s.current.Store(snap)
	return s, nil
}

// Router returns the router for requests starting now.
func (s *State) Router() Router { return s.current.Load().router }

// Describe renders the active profile and upstream for status output.
func (s *State) Describe() string { return s.current.Load().summary }

// Reload re-reads the config and swaps the state. On error the previous
// state is kept: a SIGHUP with broken JSON must never take the proxy down.
func (s *State) Reload() error {
	snap, err := loadSnapshot(s.logger)
	if err != nil {
		s.logger.Error("reload_failed", slog.String("error", err.Error()))
		return err
	}
	old := s.current.Swap(snap)
	s.logger.Info("reload",
		slog.String("from", old.profile),
		slog.String("to", snap.profile),
		slog.String("upstream", snap.summary))
	return nil
}

func loadSnapshot(logger *slog.Logger) (*snapshot, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}

	var cfg proxy.Config
	if pf.ActiveProfile != "" {
		found, ok := pf.Get(pf.ActiveProfile)
		if !ok {
			return nil, fmt.Errorf("active profile %q is not defined", pf.ActiveProfile)
		}
		cfg = found
	}
	cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)

	user, pass, deprecated, err := Resolve(cfg)
	if err != nil {
		return nil, err
	}
	if deprecated {
		logger.Warn("deprecated_password_field",
			slog.String("detail", "the plaintext \"pass\" field is deprecated; use password_file or password_env"))
	}

	router, err := NewStaticRouter(cfg, user, pass)
	if err != nil {
		return nil, err
	}

	// Read the upstream the router was built with rather than probing it
	// with a made-up host: a no-proxy entry that happened to match that
	// host would make the summary claim DIRECT for a configured profile.
	summary := "DIRECT (no active profile)"
	if up := router.Upstream(); up.Kind != KindDirect {
		summary = fmt.Sprintf("%q -> %s", pf.ActiveProfile, up.Addr)
	}
	return &snapshot{router: router, profile: pf.ActiveProfile, summary: summary}, nil
}
