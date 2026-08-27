//go:build windows

package serve

import (
	"golang.org/x/sys/windows"
)

// reloadEventName is the named event the daemon waits on. It is scoped to
// the session (Local\), not the machine, so two users logged into the same
// computer each reload their own daemon.
const reloadEventName = `Local\proxy-helper-reload`

// ReloadRequests returns a channel that receives one value per request to
// re-read the configuration, and a function that stops listening.
//
// The Windows equivalent of SIGHUP is a named event: TriggerReload opens it
// by name and signals it. Auto-reset, so each SetEvent releases exactly one
// wait — the same one-request-one-reload shape the signal has.
func ReloadRequests() (<-chan struct{}, func()) {
	out := make(chan struct{}, 1)

	name, err := windows.UTF16PtrFromString(reloadEventName)
	if err != nil {
		close(out)
		return out, func() {}
	}
	// manualReset=false, initialState=false
	event, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		close(out)
		return out, func() {}
	}

	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			// Wait on both the event and the stop signal, so the goroutine
			// cannot outlive the daemon.
			ev, waitErr := windows.WaitForSingleObject(event, 500)
			select {
			case <-done:
				return
			default:
			}
			if waitErr != nil {
				return
			}
			if ev == windows.WAIT_OBJECT_0 {
				select {
				case out <- struct{}{}:
				default: // a reload is already pending; coalesce
				}
			}
		}
	}()

	return out, func() {
		close(done)
		_ = windows.CloseHandle(event)
	}
}

// TriggerReload asks a running daemon to re-read its configuration. It
// returns nil when no daemon is running: nothing needs reloading, which is
// success, not failure.
func TriggerReload() error {
	name, err := windows.UTF16PtrFromString(reloadEventName)
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(event)
	return windows.SetEvent(event)
}
