package proxy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEscalationBinary drops an executable named "sudo" on PATH (as the
// first entry) that always fails with exitCode, writing stderrMsg to its
// stderr. It lets Finding 2's tests exercise the escalated-read paths
// deterministically, without a real sudo/pkexec prompt.
func fakeEscalationBinary(t *testing.T, stderrMsg string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf %%s %s >&2\nexit %d\n", shellQuote(stderrMsg), exitCode)
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestDryRunWritesToOut(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf}

	if err := ex.Run("git", "config", "--global", "http.proxy", "http://p:8080"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(buf.String(), "would run: git config") {
		t.Errorf("dry-run should go to Out, got: %q", buf.String())
	}
}

// TestOutDefaultsToCurrentStdout is the regression test for a subtle break:
// cmd/proxy_test.go swaps os.Stdout for a pipe after the Executor may already
// exist, so the default must be read at write time, never cached.
func TestOutDefaultsToCurrentStdout(t *testing.T) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Stdout = orig }()

	ex := &Executor{DryRun: true} // built BEFORE the swap
	os.Stdout = w

	if err := ex.Run("true"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	w.Close()

	var got bytes.Buffer
	if _, err := got.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.String(), "would run: true") {
		t.Errorf("expected the swapped stdout to receive the preview, got: %q", got.String())
	}
}

func TestEscalationDefaultsToSudo(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf}

	if err := ex.RunPrivileged("apt-get", "update"); err != nil {
		t.Fatalf("RunPrivileged: %v", err)
	}
	if !strings.Contains(buf.String(), "would run (sudo): apt-get update") {
		t.Errorf("the default must stay sudo, got: %q", buf.String())
	}
}

func TestEscalationPkexec(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf, Escalation: EscalatePkexec}

	if err := ex.RunPrivileged("apt-get", "update"); err != nil {
		t.Fatalf("RunPrivileged: %v", err)
	}
	if !strings.Contains(buf.String(), "would run (pkexec): apt-get update") {
		t.Errorf("expected a pkexec preview, got: %q", buf.String())
	}
}

// TestEscalationNoneRefuses covers the GUI's in-process executor: it must fail
// loudly rather than block on a password prompt nobody can see.
func TestEscalationNoneRefuses(t *testing.T) {
	ex := &Executor{Escalation: EscalateNone}
	if IsRoot() {
		t.Skip("running as root, nothing to escalate")
	}

	err := ex.RunPrivileged("true")
	if err == nil {
		t.Fatal("expected EscalateNone to refuse, got nil")
	}
	if !strings.Contains(err.Error(), "requires root") {
		t.Errorf("the error should say why, got: %v", err)
	}
}

// TestRemovePrivilegedFileDryRunNoneRefuses guards against a dry-run false
// "OK": a GUI validating before applying must not see success for an
// operation that cannot actually run.
func TestRemovePrivilegedFileDryRunNoneRefuses(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf, Escalation: EscalateNone}

	err := ex.RemovePrivilegedFile("/some/path")
	if err == nil {
		t.Fatal("expected EscalateNone to refuse, got nil")
	}
	if !strings.Contains(err.Error(), "requires root") {
		t.Errorf("the error should say why, got: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("expected nothing written to Out, got: %q", buf.String())
	}
}

func TestRemovePrivilegedFileDryRunDefaultsToSudo(t *testing.T) {
	var buf bytes.Buffer
	ex := &Executor{DryRun: true, Out: &buf}

	if err := ex.RemovePrivilegedFile("/some/path"); err != nil {
		t.Fatalf("RemovePrivilegedFile: %v", err)
	}
	if !strings.Contains(buf.String(), "would remove (sudo) /some/path") {
		t.Errorf("the default must stay sudo, got: %q", buf.String())
	}
}

func TestRunIncludesStderrInTheError(t *testing.T) {
	var out, errBuf bytes.Buffer
	ex := &Executor{Out: &out, Stderr: &errBuf}

	err := ex.Run("sh", "-c", "echo 'E: Unable to locate package zzz' >&2; exit 100")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Unable to locate package zzz") {
		t.Errorf("the error must carry the command's stderr, got: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 100") {
		t.Errorf("the error must keep the exit status, got: %v", err)
	}
}

// TestRunStderrStillReachesTheWriter guards the CLI: the user watching a
// terminal must keep seeing the command's own error output as it happens,
// not only folded into a returned error.
func TestRunStderrStillReachesTheWriter(t *testing.T) {
	var out, errBuf bytes.Buffer
	ex := &Executor{Out: &out, Stderr: &errBuf}

	_ = ex.Run("sh", "-c", "echo boom >&2; exit 1")

	if !strings.Contains(errBuf.String(), "boom") {
		t.Errorf("stderr should still reach the writer, got: %q", errBuf.String())
	}
}

func TestRunSucceedsQuietly(t *testing.T) {
	ex := &Executor{Out: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if err := ex.Run("true"); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestWrapWithStderrRedactsCredentials pins the one thing worth pinning: if
// redactSecrets is ever dropped from wrapWithStderr, this test fails because
// the secret survives into the error text.
func TestWrapWithStderrRedactsCredentials(t *testing.T) {
	base := errors.New("exit status 1")
	captured := "Failed to fetch http://alice:s3cr3t@proxy.corp:8080/x"

	err := wrapWithStderr(base, captured)

	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("expected the credential to be redacted, got: %v", err)
	}
	if !strings.Contains(err.Error(), "://***:***@") {
		t.Errorf("expected the redacted form in the error, got: %v", err)
	}
}

func TestWrapWithStderrEmptyReturnsOriginal(t *testing.T) {
	base := errors.New("exit status 1")

	if err := wrapWithStderr(base, ""); !errors.Is(err, base) {
		t.Errorf("expected the original error for an empty capture, got: %v", err)
	}
	if err := wrapWithStderr(base, "   \n\t  "); !errors.Is(err, base) {
		t.Errorf("expected the original error for a whitespace-only capture, got: %v", err)
	}
}

// TestReadFileMaybePrivilegedRefusesWithoutElevation covers the GUI's
// in-process executor: it must refuse a privileged read instead of shelling
// out to sudo when escalation is disabled.
func TestReadFileMaybePrivilegedRefusesWithoutElevation(t *testing.T) {
	ex := &Executor{Escalation: EscalateNone}
	if IsRoot() {
		t.Skip("running as root, nothing to escalate")
	}

	// A file no ordinary user can read; the point is the refusal, not the file.
	_, err := ex.ReadFileMaybePrivileged("/etc/shadow", true)
	if err == nil {
		t.Fatal("expected a refusal when elevation is disabled")
	}
	if strings.Contains(err.Error(), "exec") && strings.Contains(err.Error(), "sudo") {
		t.Errorf("it must refuse instead of shelling out to sudo, got: %v", err)
	}
}

// TestWrapWithStderrPreservesErrorsIs guards exit-code inspection that a
// later task depends on for pkexec: wrapping must not break errors.Is
// against the original error.
func TestWrapWithStderrPreservesErrorsIs(t *testing.T) {
	base := errors.New("exit status 100")

	err := wrapWithStderr(base, "E: Unable to locate package zzz")

	if !errors.Is(err, base) {
		t.Errorf("expected errors.Is to see through to the original error, got: %v", err)
	}
}

// TestReadFileMaybePrivilegedWrapsEscalatedStderr covers Finding 2: a
// dismissed pkexec/sudo prompt (here faked as an always-failing "sudo" on
// PATH) must surface its real, redacted stderr instead of a bare "exit
// status N" — the same guarantee Run already gives every write path.
func TestReadFileMaybePrivilegedWrapsEscalatedStderr(t *testing.T) {
	if IsRoot() {
		t.Skip("running as root, cannot force a permission-denied read")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("data"), 0o000); err != nil {
		t.Fatal(err)
	}

	fakeEscalationBinary(t, "Failed to auth http://alice:s3cr3t@proxy.corp:8080/x", 126)

	ex := &Executor{Stderr: &bytes.Buffer{}}
	_, err := ex.ReadFileMaybePrivileged(target, true)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Failed to auth") {
		t.Errorf("the error must carry the escalated command's stderr, got: %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("expected the credential to be redacted, got: %v", err)
	}
	if !strings.Contains(err.Error(), "://***:***@") {
		t.Errorf("expected the redacted form in the error, got: %v", err)
	}
}

// TestReadFileMaybePrivilegedStderrStillReachesTheWriter mirrors
// TestRunStderrStillReachesTheWriter for the escalated read path: the live
// stream must survive alongside the captured, wrapped error.
func TestReadFileMaybePrivilegedStderrStillReachesTheWriter(t *testing.T) {
	if IsRoot() {
		t.Skip("running as root, cannot force a permission-denied read")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("data"), 0o000); err != nil {
		t.Fatal(err)
	}

	fakeEscalationBinary(t, "boom", 1)

	var errBuf bytes.Buffer
	ex := &Executor{Stderr: &errBuf}
	_, _ = ex.ReadFileMaybePrivileged(target, true)

	if !strings.Contains(errBuf.String(), "boom") {
		t.Errorf("stderr should still reach the writer, got: %q", errBuf.String())
	}
}

// TestRunPrivilegedOutputWrapsEscalatedStderr covers the second half of
// Finding 2: RunPrivilegedOutput (used by snap.go's elevated read) must also
// wrap a failure with its redacted stderr instead of a bare exit status.
func TestRunPrivilegedOutputWrapsEscalatedStderr(t *testing.T) {
	if IsRoot() {
		t.Skip("running as root, nothing to escalate")
	}

	fakeEscalationBinary(t, "Failed to auth http://alice:s3cr3t@proxy.corp:8080/x", 126)

	ex := &Executor{Stderr: &bytes.Buffer{}}
	_, err := ex.RunPrivilegedOutput("snap", "get", "system", "proxy.http")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Failed to auth") {
		t.Errorf("the error must carry the escalated command's stderr, got: %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t") {
		t.Errorf("expected the credential to be redacted, got: %v", err)
	}
}

// TestRunOutputExitErrorCarriesStderr pins the trap flagged in Finding 2's
// review: RunOutput (the unprivileged read snap.go uses when elevate is
// false) must leave cmd.Stderr unset, so a failure's *exec.ExitError carries
// its own Stderr the way snap.go's "access denied" check depends on. If this
// regresses, snap.go silently stops detecting that case and misreports it as
// "not set" instead of offering to retry with elevation.
func TestRunOutputExitErrorCarriesStderr(t *testing.T) {
	ex := &Executor{}
	_, err := ex.RunOutput("sh", "-c", "echo 'access denied' >&2; exit 1")

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got: %v", err)
	}
	if !strings.Contains(string(exitErr.Stderr), "access denied") {
		t.Errorf("RunOutput must leave ExitError.Stderr populated, got: %q", exitErr.Stderr)
	}
}
