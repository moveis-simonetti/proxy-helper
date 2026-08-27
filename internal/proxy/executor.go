package proxy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Escalation picks how the Executor gains root. The zero value is the
// historical behaviour, so existing callers keep working untouched.
type Escalation int

const (
	// EscalateSudo shells out through sudo, which prompts on the terminal.
	EscalateSudo Escalation = iota
	// EscalatePkexec uses PolicyKit, which prompts through the desktop's own
	// dialog. It is what a GUI needs: sudo has no terminal to prompt on.
	EscalatePkexec
	// EscalateNone refuses to elevate. The GUI applies user-level targets
	// with this, so a target that unexpectedly needs root fails visibly
	// instead of hanging on a prompt nobody can see.
	EscalateNone
)

// name returns the command used to elevate, or "" when elevation is refused.
func (e Escalation) name() string {
	switch e {
	case EscalatePkexec:
		return "pkexec"
	case EscalateNone:
		return ""
	default:
		return "sudo"
	}
}

// Executor centralizes every side-effecting operation a target performs, so
// dry-run and privilege escalation are handled in one place instead of by
// each target individually.
type Executor struct {
	DryRun bool
	// Out receives dry-run previews and the stdout of the commands this
	// executor runs. A nil Out means os.Stdout, resolved at write time: the
	// tests swap os.Stdout for a pipe after an Executor may already exist.
	Out io.Writer
	// Stderr receives the error output of the commands this executor runs.
	// A nil Stderr means os.Stderr, resolved at write time. Whatever a
	// command writes here is also folded into the error Run returns, so a
	// GUI can show the real message instead of a bare "exit status 1".
	Stderr io.Writer
	// Escalation selects how RunPrivileged and friends gain root.
	Escalation Escalation
}

func (e *Executor) out() io.Writer {
	if e.Out != nil {
		return e.Out
	}
	return os.Stdout
}

func (e *Executor) stderr() io.Writer {
	if e.Stderr != nil {
		return e.Stderr
	}
	return os.Stderr
}

func IsRoot() bool {
	return os.Geteuid() == 0
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ReadFileMaybePrivileged reads path as the current user. If that fails with
// a permission error and elevate is true, it retries via the configured
// escalation's "cat" (prompting interactively, unless escalation is
// disabled) instead of silently reporting "not set" for a file the caller
// genuinely can't see as themselves. When escalation is disabled
// (EscalateNone), it refuses instead of shelling out, so a GUI without a
// terminal to prompt on fails visibly rather than hanging. A failure of the
// escalated read is wrapped with its (redacted) stderr, the same way Run
// does, so a dismissed pkexec prompt surfaces its real explanation instead
// of a bare "exit status 126".
func (e *Executor) ReadFileMaybePrivileged(path string, elevate bool) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil || !os.IsPermission(err) || !elevate {
		return content, err
	}
	via := e.Escalation.name()
	if via == "" {
		return nil, fmt.Errorf("reading %s requires root, but elevation is disabled", path)
	}
	cmd := exec.Command(via, "cat", path)
	cmd.Stdin = os.Stdin
	var captured bytes.Buffer
	cmd.Stderr = io.MultiWriter(e.stderr(), &captured)
	out, err := cmd.Output()
	if err != nil {
		return out, wrapWithStderr(err, captured.String())
	}
	return out, nil
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "      " + l
	}
	return strings.Join(lines, "\n")
}

// redactSecrets masks credentials in text that's about to be printed for a
// dry-run preview: userinfo embedded in URLs (proxy://user:pass@host) and
// JSON fields that hold tokens/passwords (docker's "auth", "identitytoken", ...).
var (
	urlUserinfoRe = regexp.MustCompile(`://[^/@\s"]+:[^/@\s"]+@`)
	jsonSecretRe  = regexp.MustCompile(`(?i)("(?:auth|identitytoken|password|authentication-password)"\s*:\s*")[^"]*(")`)
)

func redactSecrets(s string) string {
	s = urlUserinfoRe.ReplaceAllString(s, "://***:***@")
	s = jsonSecretRe.ReplaceAllString(s, "${1}***REDACTED***${2}")
	return s
}

// sensitiveArgKeys marks command-line flags/keys whose *next* argument is a
// secret and must be masked outright, since it won't match a URL or JSON pattern.
var sensitiveArgKeys = map[string]bool{
	"authentication-password": true,
}

func redactArgs(args []string) []string {
	out := make([]string, len(args))
	redactNext := false
	for i, a := range args {
		if redactNext {
			out[i] = "***REDACTED***"
			redactNext = false
			continue
		}
		out[i] = redactSecrets(a)
		if sensitiveArgKeys[a] {
			redactNext = true
		}
	}
	return out
}

// wrapWithStderr folds a failed command's error output into err, redacted:
// a failing command can echo the proxy URL with its credentials, and this
// text reaches error dialogs and logs. It returns err unchanged when
// captured is empty or whitespace-only, and always preserves err as the
// wrapped cause so errors.Is/As still see through to it (exit codes, in
// particular).
func wrapWithStderr(err error, captured string) error {
	msg := strings.TrimSpace(captured)
	if msg == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, redactSecrets(msg))
}

// Warn prints an informational message to the same stream a command's
// stderr goes to, without treating it as a failure. Targets use it when
// skipping an optional step instead of either failing the whole operation
// or doing nothing silently.
func (e *Executor) Warn(format string, args ...any) {
	fmt.Fprintf(e.stderr(), "  [warn] "+format+"\n", args...)
}

// Run executes a command as the current user.
func (e *Executor) Run(name string, args ...string) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would run: %s %s\n", name, strings.Join(redactArgs(args), " "))
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = e.out()
	var captured bytes.Buffer
	cmd.Stderr = io.MultiWriter(e.stderr(), &captured)
	if err := cmd.Run(); err != nil {
		return wrapWithStderr(err, captured.String())
	}
	return nil
}

// RunPrivileged executes a command as root through the configured escalation
// (which will prompt), unless already running as root.
func (e *Executor) RunPrivileged(name string, args ...string) error {
	via := e.Escalation.name()
	if e.DryRun {
		if via == "" {
			return fmt.Errorf("%s requires root, but elevation is disabled", name)
		}
		fmt.Fprintf(e.out(), "  [dry-run] would run (%s): %s %s\n", via, name, strings.Join(redactArgs(args), " "))
		return nil
	}
	if IsRoot() {
		return e.Run(name, args...)
	}
	if via == "" {
		return fmt.Errorf("%s requires root, but elevation is disabled", name)
	}
	full := append([]string{name}, args...)
	return e.Run(via, full...)
}

// WriteFile writes content to a user-owned path, creating parent dirs as needed.
func (e *Executor) WriteFile(path string, content []byte, perm os.FileMode) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would write %s:\n%s\n", path, indent(redactSecrets(string(content))))
		return nil
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content, perm)
}

// WriteFilePreview writes content, but shows only preview in a dry-run.
//
// It exists for files the tool edits surgically: printing the whole file, the
// way WriteFile does, dumps everything the user keeps in it. A shell rc is the
// worst case — it routinely holds unrelated API tokens, and redactSecrets only
// masks URL userinfo and JSON secret fields, not "export TOKEN=...". The
// preview shows the part this tool is responsible for and nothing else.
func (e *Executor) WriteFilePreview(path string, content []byte, preview string, perm os.FileMode) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would update %s, changing only this block:\n%s\n", path, indent(redactSecrets(preview)))
		return nil
	}
	return e.WriteFile(path, content, perm)
}

// WritePrivilegedFile writes content to a root-owned path via sudo tee.
func (e *Executor) WritePrivilegedFile(path string, content []byte, perm os.FileMode) error {
	via := e.Escalation.name()
	if e.DryRun {
		if via == "" {
			return fmt.Errorf("writing %s requires root, but elevation is disabled", path)
		}
		fmt.Fprintf(e.out(), "  [dry-run] would write (%s) %s:\n%s\n", via, path, indent(redactSecrets(string(content))))
		return nil
	}
	if IsRoot() {
		if dir := filepath.Dir(path); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		return os.WriteFile(path, content, perm)
	}
	if via == "" {
		return fmt.Errorf("writing %s requires root, but elevation is disabled", path)
	}
	mkdir := exec.Command(via, "mkdir", "-p", filepath.Dir(path))
	var mkdirErr bytes.Buffer
	mkdir.Stdout = e.out()
	mkdir.Stderr = io.MultiWriter(e.stderr(), &mkdirErr)
	if err := mkdir.Run(); err != nil {
		return wrapWithStderr(err, mkdirErr.String())
	}
	tee := exec.Command(via, "tee", path)
	tee.Stdin = bytes.NewReader(content)
	tee.Stdout = io.Discard
	var teeErr bytes.Buffer
	tee.Stderr = io.MultiWriter(e.stderr(), &teeErr)
	if err := tee.Run(); err != nil {
		return wrapWithStderr(err, teeErr.String())
	}
	return nil
}

// RunOutput runs name as the current user and returns its captured stdout,
// for reads that need to inspect a command's output rather than just its
// success (e.g. `snap get ...`). Stderr is left unset on the command so a
// failure's *exec.ExitError carries the command's stderr the way callers
// that inspect it expect, matching exec.Cmd.Output's own documented
// behaviour.
func (e *Executor) RunOutput(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// RunPrivilegedOutput runs name via the configured escalation and returns
// its captured stdout, for reads that need root even to inspect state (e.g.
// `snap get system ...`, which denies unprivileged reads outright). It
// mirrors RunPrivileged's escalation handling but captures output instead of
// streaming it to Out, and refuses instead of shelling out when escalation
// is disabled (EscalateNone). On failure of the escalated path it wraps the
// error with its (redacted) captured stderr, same as Run — the ExitError
// returned there no longer carries its own .Stderr (cmd.Stderr is non-nil),
// unlike RunOutput; callers that need to inspect ExitError.Stderr directly
// (see snap.go) must go through RunOutput's unprivileged path instead.
func (e *Executor) RunPrivilegedOutput(name string, args ...string) ([]byte, error) {
	if IsRoot() {
		return exec.Command(name, args...).Output()
	}
	via := e.Escalation.name()
	if via == "" {
		return nil, fmt.Errorf("%s requires root, but elevation is disabled", name)
	}
	full := append([]string{name}, args...)
	cmd := exec.Command(via, full...)
	cmd.Stdin = os.Stdin
	var captured bytes.Buffer
	cmd.Stderr = io.MultiWriter(e.stderr(), &captured)
	out, err := cmd.Output()
	if err != nil {
		return out, wrapWithStderr(err, captured.String())
	}
	return out, nil
}

// RemoveFile deletes a user-owned path. Missing files are not an error.
func (e *Executor) RemoveFile(path string) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would remove %s\n", path)
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RemovePrivilegedFile deletes a root-owned path via sudo. Missing files are not an error.
func (e *Executor) RemovePrivilegedFile(path string) error {
	if e.DryRun {
		via := e.Escalation.name()
		if via == "" {
			return fmt.Errorf("removing %s requires root, but elevation is disabled", path)
		}
		fmt.Fprintf(e.out(), "  [dry-run] would remove (%s) %s\n", via, path)
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return e.RunPrivileged("rm", "-f", path)
}

// Redact masks credentials in text that is about to be printed. It is
// exported so the log renderer can apply the same rules as dry-run previews.
func Redact(s string) string { return redactSecrets(s) }
