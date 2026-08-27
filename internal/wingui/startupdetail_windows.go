//go:build windows

package wingui

import (
	"os"

	"proxy-helper/internal/serve"
)

// daemonStartupDetail returns what the daemon last complained about, or an
// empty string when it left nothing behind.
func daemonStartupDetail() string {
	path, err := serve.LogFilePath()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return summarizeLog(string(raw))
}

// daemonLogLocation is where to look when the summary is not enough.
func daemonLogLocation() string {
	path, err := serve.LogFilePath()
	if err != nil {
		return ""
	}
	return path
}
