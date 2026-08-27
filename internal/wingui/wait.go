package wingui

import (
	"time"

	"proxy-helper/internal/serve"
)

// daemonStartTimeout bounds how long EnsureDaemon waits for the freshly
// started proxy to accept connections.
//
// The task starts a process; the process then binds a port. Returning
// between those two moments would report success while the next step —
// pointing the system proxy at that port — is still doomed.
const daemonStartTimeout = 8 * time.Second

// waitForDaemon polls until the daemon answers, or the timeout expires.
func waitForDaemon() bool {
	deadline := time.Now().Add(daemonStartTimeout)
	for time.Now().Before(deadline) {
		if serve.DaemonActive() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return serve.DaemonActive()
}
