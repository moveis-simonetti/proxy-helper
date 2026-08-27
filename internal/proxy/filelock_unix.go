//go:build !windows

package proxy

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f and returns the function
// that releases it. Unix has flock(2); Windows has no equivalent syscall,
// which is why this is split per platform rather than called inline.
func lockFile(f *os.File) (unlock func(), err error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
