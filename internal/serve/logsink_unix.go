//go:build !windows

package serve

import (
	"io"
	"os"
)

// LogSink returns where the daemon writes its structured log, and a function
// to close it.
//
// On Unix that is stdout: systemd captures it into the journal, which
// handles rotation, retention and querying. Windows has no such service, so
// the platform split starts here.
func LogSink() (io.Writer, func() error, error) {
	return os.Stdout, func() error { return nil }, nil
}
