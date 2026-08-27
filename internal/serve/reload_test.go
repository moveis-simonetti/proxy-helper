//go:build !windows

package serve

import (
	"syscall"
	"testing"
	"time"
)

// The Windows half of this cannot run here. What these lock down is the
// contract both halves must honour, so the Windows implementation has
// something to be written against.

func TestReloadRequestsDeliversOnSignal(t *testing.T) {
	reloads, stop := ReloadRequests()
	defer stop()

	// Give signal.Notify a moment to register before raising.
	time.Sleep(20 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("raising SIGHUP: %v", err)
	}

	select {
	case <-reloads:
	case <-time.After(2 * time.Second):
		t.Fatal("no reload request arrived; the daemon would accept a new config and never apply it")
	}
}

func TestReloadRequestsStopsDelivering(t *testing.T) {
	reloads, stop := ReloadRequests()
	stop()

	// After stop the channel is closed, so a receive returns immediately
	// with the zero value rather than blocking forever. A goroutine that
	// outlived the daemon would keep a signal handler registered.
	select {
	case _, open := <-reloads:
		if open {
			t.Error("received a reload request after stop()")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel neither closed nor delivered after stop(); the listener leaked")
	}
}

func TestReloadRequestsCoalescesABurst(t *testing.T) {
	// Reload re-reads a file; ten signals in a row need not mean ten reads,
	// and an unbuffered hand-off would block the signal goroutine instead.
	reloads, stop := ReloadRequests()
	defer stop()

	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
			t.Fatalf("raising SIGHUP: %v", err)
		}
	}

	select {
	case <-reloads:
	case <-time.After(2 * time.Second):
		t.Fatal("no reload request arrived after a burst")
	}
	// The point is that the burst neither blocks nor panics; how many
	// requests survive coalescing is deliberately not asserted.
}
