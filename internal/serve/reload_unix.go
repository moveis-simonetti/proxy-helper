//go:build !windows

package serve

import (
	"os"
	"os/signal"
	"syscall"
)

// ReloadRequests returns a channel that receives one value per request to
// re-read the configuration, and a function that stops listening.
//
// On Unix that request is SIGHUP, which is what "systemctl --user reload"
// sends. Windows has no signals, so this is split per platform rather than
// wired inline — and the split is not cosmetic: syscall.SIGHUP *compiles*
// on Windows and simply never fires, so a shared implementation would leave
// the daemon accepting a new configuration it never applies, with no error
// anywhere.
func ReloadRequests() (<-chan struct{}, func()) {
	raw := make(chan os.Signal, 1)
	signal.Notify(raw, syscall.SIGHUP)

	out := make(chan struct{}, 1)
	go func() {
		for range raw {
			select {
			case out <- struct{}{}:
			default: // a reload is already pending; coalesce
			}
		}
		close(out)
	}()

	return out, func() { signal.Stop(raw); close(raw) }
}
