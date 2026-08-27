//go:build windows

package proxy

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The registry is to the Windows targets what a config file is to the Unix
// ones, so it goes through the Executor for the same reason: --dry-run has
// to be implemented in one place, and a target reaching for the registry API
// directly would write for real during a dry run.
//
// Every path here is under HKEY_CURRENT_USER; there is deliberately no
// machine-wide variant, because nothing in this build needs one and adding
// it would introduce the elevation this product does without.

// SetRegistryString writes a string value under HKCU.
func (e *Executor) SetRegistryString(path, name, value string) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would set HKCU\\%s\\%s = %q\n", path, name, redactSecrets(value))
		return nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s: %w", path, err)
	}
	defer key.Close()
	if err := key.SetStringValue(name, value); err != nil {
		return fmt.Errorf("setting HKCU\\%s\\%s: %w", path, name, err)
	}
	return nil
}

// SetRegistryUint32 writes a DWORD value under HKCU.
func (e *Executor) SetRegistryUint32(path, name string, value uint32) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would set HKCU\\%s\\%s = %d\n", path, name, value)
		return nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s: %w", path, err)
	}
	defer key.Close()
	if err := key.SetDWordValue(name, value); err != nil {
		return fmt.Errorf("setting HKCU\\%s\\%s: %w", path, name, err)
	}
	return nil
}

// DeleteRegistryValue removes a value under HKCU. A value that is already
// absent is not an error: Unset runs against whatever state the machine is
// in, and "it was already gone" is success.
func (e *Executor) DeleteRegistryValue(path, name string) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would delete HKCU\\%s\\%s\n", path, name)
		return nil
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening HKCU\\%s: %w", path, err)
	}
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("deleting HKCU\\%s\\%s: %w", path, name, err)
	}
	return nil
}

const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
)

// RefreshWinINET tells running processes to re-read the proxy settings.
//
// Without it the registry is correct but an already-open browser keeps using
// the old settings until it restarts — which a user reads as the app having
// done nothing at all. Both options are needed: SETTINGS_CHANGED announces
// that the values changed, REFRESH makes WinINET reload them.
func (e *Executor) RefreshWinINET() error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would notify running programs that the proxy settings changed\n")
		return nil
	}

	wininet := windows.NewLazySystemDLL("wininet.dll")
	setOption := wininet.NewProc("InternetSetOptionW")

	for _, option := range []uintptr{internetOptionSettingsChanged, internetOptionRefresh} {
		ret, _, err := setOption.Call(0, option, uintptr(unsafe.Pointer(nil)), 0)
		if ret == 0 {
			return fmt.Errorf("notifying programs of the proxy change: %w", err)
		}
	}
	return nil
}

// GetRegistryString reads a string value under HKCU. A missing value is not
// an error: callers ask in order to find out.
func (e *Executor) GetRegistryString(path, name string) (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return "", false
	}
	return value, true
}

// StartDetached launches a program that must outlive this process.
//
// The daemon used to be started through Task Scheduler, which needs a
// permission a managed Windows account often does not have — "ERRO: Acesso
// negado" with nothing the person can do about it. Starting the process
// directly needs no permission at all: it is the same thing the user could
// do by double-clicking the binary.
//
// DETACHED_PROCESS and no inherited handles are what make it survive: the
// interface can be closed, and the proxy keeps answering.
func (e *Executor) StartDetached(name string, args ...string) error {
	if e.DryRun {
		fmt.Fprintf(e.out(), "  [dry-run] would start: %s %s\n", name, strings.Join(redactArgs(args), " "))
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	// Not waited on deliberately: this is a daemon, and Wait would block
	// until it exits. Releasing lets this process forget about it.
	return cmd.Process.Release()
}
