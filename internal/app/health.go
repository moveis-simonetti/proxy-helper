package app

import "proxy-helper/internal/proxy"

// DaemonHealth is what a caller needs to tell a working setup from a machine
// with no network at all.
type DaemonHealth struct {
	// ViaLocal is whether the targets point at the daemon. It is what makes
	// a stopped daemon matter: without it they carry the upstream
	// themselves and a stopped daemon costs nothing.
	ViaLocal bool
	Port     int
	// Listening is what actually answered on the daemon's port.
	Listening []string
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

// CheckDaemon probes the daemon's port and reports what it found.
//
// probe is injected rather than called directly so this stays testable, and
// because the probe is the authority: systemd reporting the unit "active"
// says the process exists, not that it managed to bind. A daemon that lost
// its port to something else, or is mid-restart, is active and useless.
func CheckDaemon(pf *proxy.ProfileFile, probe func(port int) []string) DaemonHealth {
	port := pf.EffectiveLocalPort()
	return DaemonHealth{
		ViaLocal:  pf.ViaLocal,
		Port:      port,
		Listening: probe(port),
	}
}
