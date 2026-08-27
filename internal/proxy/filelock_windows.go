//go:build windows

package proxy

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on f and returns the function that
// releases it, mirroring the flock(2) call the Unix build uses.
//
// LockFileEx is mandatory rather than advisory, and without
// LOCKFILE_FAIL_IMMEDIATELY it blocks until the lock is free — matching
// LOCK_EX's behaviour, which is what the read-modify-write cycle in
// withProfileFileLock depends on.
func lockFile(f *os.File) (unlock func(), err error) {
	handle := windows.Handle(f.Fd())
	overlapped := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return nil, err
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, new(windows.Overlapped))
	}, nil
}
