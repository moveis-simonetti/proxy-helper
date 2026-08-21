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

// Executor centralizes every side-effecting operation a target performs, so
// dry-run and privilege escalation are handled in one place instead of by
// each target individually.
type Executor struct {
	DryRun bool
}

func IsRoot() bool {
	return os.Geteuid() == 0
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// readFileMaybePrivileged reads path as the current user. If that fails
// with a permission error and elevate is true, it retries via sudo cat
// (prompting interactively) instead of silently reporting "not set" for a
// file the caller genuinely can't see as themselves.
func readFileMaybePrivileged(path string, elevate bool) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err == nil || !os.IsPermission(err) || !elevate {
		return content, err
	}
	cmd := exec.Command("sudo", "cat", path)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	return cmd.Output()
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

// Run executes a command as the current user.
func (e *Executor) Run(name string, args ...string) error {
	if e.DryRun {
		fmt.Printf("  [dry-run] would run: %s %s\n", name, strings.Join(redactArgs(args), " "))
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunPrivileged executes a command as root, shelling out through sudo
// (which will prompt interactively) unless already running as root.
func (e *Executor) RunPrivileged(name string, args ...string) error {
	if e.DryRun {
		fmt.Printf("  [dry-run] would run (sudo): %s %s\n", name, strings.Join(redactArgs(args), " "))
		return nil
	}
	if IsRoot() {
		return e.Run(name, args...)
	}
	full := append([]string{name}, args...)
	return e.Run("sudo", full...)
}

// WriteFile writes content to a user-owned path, creating parent dirs as needed.
func (e *Executor) WriteFile(path string, content []byte, perm os.FileMode) error {
	if e.DryRun {
		fmt.Printf("  [dry-run] would write %s:\n%s\n", path, indent(redactSecrets(string(content))))
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
		fmt.Printf("  [dry-run] would update %s, changing only this block:\n%s\n", path, indent(redactSecrets(preview)))
		return nil
	}
	return e.WriteFile(path, content, perm)
}

// WritePrivilegedFile writes content to a root-owned path via sudo tee.
func (e *Executor) WritePrivilegedFile(path string, content []byte, perm os.FileMode) error {
	if e.DryRun {
		fmt.Printf("  [dry-run] would write (sudo) %s:\n%s\n", path, indent(redactSecrets(string(content))))
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
	mkdir := exec.Command("sudo", "mkdir", "-p", filepath.Dir(path))
	mkdir.Stdout, mkdir.Stderr = os.Stdout, os.Stderr
	if err := mkdir.Run(); err != nil {
		return err
	}
	tee := exec.Command("sudo", "tee", path)
	tee.Stdin = bytes.NewReader(content)
	tee.Stdout = io.Discard
	tee.Stderr = os.Stderr
	return tee.Run()
}

// RemoveFile deletes a user-owned path. Missing files are not an error.
func (e *Executor) RemoveFile(path string) error {
	if e.DryRun {
		fmt.Printf("  [dry-run] would remove %s\n", path)
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
		fmt.Printf("  [dry-run] would remove (sudo) %s\n", path)
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
