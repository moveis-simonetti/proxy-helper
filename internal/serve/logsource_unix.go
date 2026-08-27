//go:build !windows

package serve

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
// On Unix the journal is the store, so this shells out to journalctl. The
// caller does not need to know that: it reads lines and hands them to
// ParseEntries either way.
func OpenLogStream(q LogQuery) (io.ReadCloser, func() error, error) {
	args := []string{"--user", "-u", UnitName, "-o", "json", "--no-pager"}
	if q.Follow {
		args = append(args, "-f")
	}
	if q.Since != "" {
		args = append(args, "--since", q.Since)
	} else {
		args = append(args, "-n", fmt.Sprint(q.Lines))
	}

	cmd := exec.Command("journalctl", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("running journalctl: %w", err)
	}
	return out, cmd.Wait, nil
}
