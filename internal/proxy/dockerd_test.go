package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDockerdStatusUnavailableReportsReason covers Finding 1: an unavailable
// dockerd target must report why (a real reason, not the "not set" that a
// blind fall-through into the file read would produce), and must not
// perform the file read at all. PATH is pointed at an empty directory so
// commandExists("systemctl")/commandExists("docker") both fail, forcing
// Available() to false deterministically regardless of the host.
func TestDockerdStatusUnavailableReportsReason(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	tgt := NewDockerdTarget()
	if tgt.Available() {
		t.Fatal("expected Available() to be false with an empty PATH")
	}

	// EscalateNone with elevate=true: if Status fell through to
	// ReadFileMaybePrivileged despite being unavailable, a permission error
	// on the real system path would trip this and return the "elevation is
	// disabled" error instead of a nil error with a reason.
	ex := &Executor{Escalation: EscalateNone}
	st, err := tgt.Status(ex, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if st.Available {
		t.Fatal("expected Available to be false")
	}
	if st.Enabled {
		t.Error("expected Enabled to be false for an unavailable target")
	}
	if st.Detail == "" || st.Detail == "not set" {
		t.Errorf("expected a real unavailability reason, got: %q", st.Detail)
	}
}

// TestDockerdAvailableWhenBothCommandsPresent is a light sanity check that
// the PATH trick above is actually exercising the intended condition, not
// something else.
func TestDockerdAvailableWhenBothCommandsPresent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"systemctl", "docker"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	tgt := NewDockerdTarget()
	if !tgt.Available() {
		t.Fatal("expected Available() to be true when both commands are on PATH")
	}
}
