//go:build windows

package serve

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// maxLogBytes is when the log file is rotated. Journald applies its own
// retention on Unix; here it is ours to enforce, and an unbounded log on a
// machine nobody administers is a disk that eventually fills.
const maxLogBytes = 2 * 1024 * 1024

// LogFilePath is where the daemon's log lives.
func LogFilePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "proxy-helper", "daemon.log"), nil
}

// LogSink returns where the daemon writes its structured log, and a function
// to close it.
//
// There is no journald here, so the daemon keeps its own file: same JSON,
// one object per line, which is what ParseEntries already reads.
func LogSink() (io.Writer, func() error, error) {
	path, err := LogFilePath()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the log file: %w", err)
	}
	w := &rotatingFile{file: f, path: path}
	return w, w.Close, nil
}

// rotatingFile keeps one previous generation and nothing older. Two files
// bound the disk cost while still surviving a rotation that happens right
// before the problem someone is trying to diagnose.
type rotatingFile struct {
	mu   sync.Mutex
	file *os.File
	path string
	size int64
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.size == 0 {
		if info, err := r.file.Stat(); err == nil {
			r.size = info.Size()
		}
	}
	if r.size+int64(len(p)) > maxLogBytes {
		if err := r.rotate(); err != nil {
			// Losing the rotation must not lose the log line: keep writing
			// to the file we have rather than failing the daemon's logger.
			_ = err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	// Windows will not rename onto an existing file.
	_ = os.Remove(r.path + ".1")
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	r.file = f
	r.size = 0
	return nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file.Close()
}
