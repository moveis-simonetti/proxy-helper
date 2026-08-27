package proxy

import (
	"io"
	"strings"
	"testing"
)

// The Windows GUI has no console, so its executor discards output. The
// error still has to say what the command complained about — schtasks
// writes its failures to stdout, and a bare "exit status 1" is unactionable
// for the person who sees it.
func TestRunCapturedReportsOutputEvenWhenItIsDiscarded(t *testing.T) {
	ex := &Executor{Out: io.Discard, Stderr: io.Discard}

	err := ex.RunCaptured("sh", "-c", "echo 'ERROR: task name is invalid'; exit 1")

	if err == nil {
		t.Fatal("a failing command returned no error")
	}
	if !strings.Contains(err.Error(), "task name is invalid") {
		t.Errorf("error = %q, want it to carry what the command printed", err)
	}
}

// Run captured only stderr, which is why schtasks — a program that reports
// errors on stdout — produced errors with nothing in them.
func TestRunCapturedIncludesStdoutWhichRunDoesNot(t *testing.T) {
	script := "echo 'only on stdout'; exit 1"

	runErr := (&Executor{Out: io.Discard, Stderr: io.Discard}).Run("sh", "-c", script)
	capturedErr := (&Executor{Out: io.Discard, Stderr: io.Discard}).RunCaptured("sh", "-c", script)

	if strings.Contains(runErr.Error(), "only on stdout") {
		t.Error("Run now captures stdout; this test no longer describes the difference")
	}
	if !strings.Contains(capturedErr.Error(), "only on stdout") {
		t.Errorf("RunCaptured = %q, want it to carry the stdout message", capturedErr)
	}
}
