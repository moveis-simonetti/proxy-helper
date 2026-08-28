//go:build windows

package wingui

import "proxy-helper/internal/serve"

// daemonAutostartRegistered reports whether the proxy will come back after
// the computer is restarted.
func daemonAutostartRegistered() bool {
	return serve.AutostartRegistered(executor())
}
