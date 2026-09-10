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

	// reachable is the prober's verdict on the upstream, and it lives here
	// rather than inside snapshot on purpose: the prober would otherwise
	// have to build a snapshot to publish a verdict, racing Reload for the
	// same pointer. Kept out, the two never contend — Reload owns the
	// snapshot, the prober owns this bool, and Router() reads both.
	//
	// It starts true so a daemon that has not probed yet forwards rather
	// than degrading. Never fail closed on missing information.
	reachable atomic.Bool

	// kick wakes the prober when Reload changed the upstream. Capacity 1
	// and a non-blocking send: a pending wake-up is as good as two, and
	// Reload must never block on a prober that is mid-dial.
	kick chan struct{}

	// port is only carried so the published runtime state can name it; the
	// daemon, not State, decides where to listen.
	port atomic.Int64
}

type snapshot struct {
	router  Router
	profile string
	summary string
	mode    proxy.Mode
	// upstreamAddr is what the prober dials. Empty in direct mode, and
	// empty when no profile is selected — in both cases there is nothing
	// to probe.
	upstreamAddr string
}

// directRouter sends everything straight out. It is a type of its own rather
// than a zero-value StaticRouter: that one's exact map is nil, which works
// only by accident of Go's nil-map reads and would break silently the day
// Route grows a write.
type directRouter struct{}

func (directRouter) Route(string) Upstream { return Upstream{} }

// NewState loads the current configuration. It fails only if the config is
// unreadable at startup; a broken config during Reload is non-fatal.
func NewState(logger *slog.Logger) (*State, error) {
	s := &State{logger: logger, kick: make(chan struct{}, 1)}
	snap, err := loadSnapshot(logger)
	if err != nil {
		return nil, err
	}
	s.reachable.Store(true)
	s.current.Store(snap)
	return s, nil
}

// Router returns the router for requests starting now.
//
// This is the request path: no locks, two atomic loads. The mode decides
// whether the configured upstream is consulted at all, and only auto lets
// the prober's verdict override it — upstream is a strict pin, so a machine
// that must never bypass the proxy keeps forwarding even while the upstream
// looks dead.
func (s *State) Router() Router {
	snap := s.current.Load()
	switch snap.mode {
	case proxy.ModeDirect:
		return directRouter{}
	case proxy.ModeAuto:
		if !s.reachable.Load() {
			return directRouter{}
		}
	}
	return snap.router
}

// Mode reports the configured routing mode.
func (s *State) Mode() proxy.Mode { return s.current.Load().mode }

// UpstreamAddr is the host:port the prober should dial, or empty when there
// is nothing to probe.
func (s *State) UpstreamAddr() string { return s.current.Load().upstreamAddr }

// Reachable reports the prober's current verdict.
func (s *State) Reachable() bool { return s.reachable.Load() }

// setReachable records a verdict. Unexported: only the prober and tests set
// it, never a request.
func (s *State) setReachable(up bool) { s.reachable.Store(up) }

// SetPort records the port the daemon listens on, for the published runtime
// state. Call it before the first publish.
func (s *State) SetPort(p int) { s.port.Store(int64(p)) }

// Describe renders the mode, active profile and upstream for status output.
func (s *State) Describe() string { return s.current.Load().summary }

// publish refreshes the runtime state file, logging rather than failing:
// nobody outside can read the state, but the proxy itself still works, and
// taking the daemon down over a status file would be the wrong trade.
func (s *State) publish() {
	if err := s.PublishRuntimeState(); err != nil {
		s.logger.Warn("runtime_state_write_failed", slog.String("error", err.Error()))
	}
}

// Reload re-reads the config and swaps the state. On error the previous
// state is kept: a SIGHUP with broken JSON must never take the proxy down.
func (s *State) Reload() error {
	snap, err := loadSnapshot(s.logger)
	if err != nil {
		s.logger.Error("reload_failed", slog.String("error", err.Error()))
		return err
	}
	old := s.current.Swap(snap)
	// A different upstream has not been probed yet, so the old one's
	// verdict says nothing about it. Carrying a stale "down" over would
	// pin a perfectly healthy new upstream to direct until the next probe
	// interval elapsed.
	if snap.upstreamAddr != old.upstreamAddr {
		s.reachable.Store(true)
		s.wake()
	}
	s.logger.Info("reload",
		slog.String("from", old.profile),
		slog.String("to", snap.profile),
		slog.String("mode", string(snap.mode)),
		slog.String("upstream", snap.summary))
	s.publish()
	return nil
}

// wake nudges the prober without ever blocking the caller.
func (s *State) wake() {
	if s.kick == nil {
		return
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

func loadSnapshot(logger *slog.Logger) (*snapshot, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}

	mode := pf.EffectiveMode()

	// The profile is loaded whenever one is selected, even in direct mode:
	// a typo in active_profile should surface as an error the user can see
	// rather than silently looking like "off".
	var cfg proxy.Config
	if pf.ActiveProfile != "" {
		found, ok := pf.Get(pf.ActiveProfile)
		if !ok {
			return nil, fmt.Errorf("active profile %q is not defined", pf.ActiveProfile)
		}
		if mode != proxy.ModeDirect {
			cfg = found
		}
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
	summary := fmt.Sprintf("DIRECT (mode %s)", mode)
	upstreamAddr := ""
	if up := router.Upstream(); up.Kind != KindDirect {
		upstreamAddr = up.Addr
		summary = fmt.Sprintf("mode %s, %q -> %s", mode, pf.ActiveProfile, up.Addr)
	}
	return &snapshot{
		router:       router,
		profile:      pf.ActiveProfile,
		summary:      summary,
		mode:         mode,
		upstreamAddr: upstreamAddr,
	}, nil
}
