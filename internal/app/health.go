package app

import (
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// DaemonChecks are the observations CheckDaemon needs. They are injected so
// the decision logic stays testable, and because each is a different kind of
// evidence that must not be confused with the others.
type DaemonChecks struct {
	// Listening probes the port. It is the authority on whether anything is
	// bound — see CheckDaemon.
	Listening func(port int) []string
	// Published reports the PID the daemon wrote into its runtime state
	// file, and whether there was a file at all.
	Published func() (pid int, ok bool)
	// MainPID is what systemd says is running the unit, or 0 when there is
	// no systemd or no unit.
	MainPID func() int
}

// DaemonHealth is what a caller needs to tell a working setup from a machine
// with no network at all, or from one running a daemon that predates the
// routing mode.
type DaemonHealth struct {
	// ViaLocal is whether the targets point at the daemon. It is what makes
	// a stopped daemon matter: without it they carry the upstream
	// themselves and a stopped daemon costs nothing.
	ViaLocal bool
	Port     int
	// Listening is what actually answered on the daemon's port.
	Listening []string
	// publishedPID / published / mainPID back Outdated. Unexported: they
	// are evidence for one question, not facts a caller should reinterpret.
	publishedPID int
	published    bool
	mainPID      int
}

// Stranded reports the state this whole check exists for: every target
// pointing at a daemon that is not there, so nothing on the machine can
// reach the network.
//
// It is the cost of the cheap toggle. Pointing thirteen targets at one
// local daemon is what makes switching instant and password-free, and it
// also makes that daemon a single point of failure. systemd's Restart=always
// covers the ordinary crash; this covers the rest, by making the failure
// legible instead of leaving the user with a machine that is simply offline.
func (h DaemonHealth) Stranded() bool {
	return h.ViaLocal && len(h.Listening) == 0
}

// Outdated reports that the daemon answering on the port is not the one that
// published the runtime state — which in practice means it is a build from
// before the state file existed, still running because replacing the binary
// on disk does not restart a running process.
//
// This matters far more than it sounds. A daemon from before the mode field
// reads active_profile to decide whether to forward, and the current
// semantics deliberately keep a profile selected while routing direct. So
// the old daemon sees a profile, forwards, and every toggle the user makes
// writes config and sends a SIGHUP that changes nothing at all. Without this
// check the failure is completely silent.
//
// The PID comparison is what makes it precise: a state file left behind by a
// killed daemon names a PID the unit no longer has, and trusting the file's
// mere existence would report a healthy daemon that is not there.
//
// It stays false without systemd (MainPID 0) — a daemon run by hand in a
// terminal publishes state but has no unit, and nagging about that would
// punish exactly the person debugging it. It also stays false when stranded:
// nothing listening is a different and louder problem.
func (h DaemonHealth) Outdated() bool {
	if !h.ViaLocal || h.Stranded() || h.mainPID == 0 {
		return false
	}
	return !h.published || h.publishedPID != h.mainPID
}

// CheckDaemon gathers what is observable about the daemon and reports it.
//
// The probe is the authority on whether anything is bound: systemd reporting
// the unit "active" says the process exists, not that it managed to bind. A
// daemon that lost its port to something else, or is mid-restart, is active
// and useless.
func CheckDaemon(pf *proxy.ProfileFile, c DaemonChecks) DaemonHealth {
	port := pf.EffectiveLocalPort()
	h := DaemonHealth{
		ViaLocal:  pf.ViaLocal,
		Port:      port,
		Listening: c.Listening(port),
	}
	if c.Published != nil {
		h.publishedPID, h.published = c.Published()
	}
	if c.MainPID != nil {
		h.mainPID = c.MainPID()
	}
	return h
}

// LiveDaemonChecks wires the real observations. Both the CLI and the GUI use
// it, so the two can never disagree about what "the daemon is fine" means.
func LiveDaemonChecks() DaemonChecks {
	return DaemonChecks{
		Listening: serve.ListeningAddrs,
		Published: func() (int, bool) {
			rs, err := serve.ReadRuntimeState()
			if err != nil || !rs.Running {
				return 0, false
			}
			return rs.PID, true
		},
		MainPID: serve.DaemonMainPID,
	}
}
