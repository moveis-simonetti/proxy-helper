//go:build windows

package serve

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// LogQuery describes which log lines the caller wants.
type LogQuery struct {
	Follow bool
	Since  string
	Lines  int
}

// OpenLogStream returns the daemon's log as a stream of JSON lines, plus a
// function that releases whatever backs it.
//
// Windows has no journal, so the daemon's own file is the store. Since and
// Lines are not applied here: the caller already filters parsed entries by
// time, and a file this size is cheap to read whole — doing it in two places
// would let the two disagree about what "since" means.
func OpenLogStream(q LogQuery) (io.ReadCloser, func() error, error) {
	path, err := LogFilePath()
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		// Not an error worth failing on: the daemon may simply not have run
		// yet. An empty stream renders as "no entries", which is true.
		return io.NopCloser(strings.NewReader("")), func() error { return nil }, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("opening the log file: %w", err)
	}
	return f, func() error { return nil }, nil
}
