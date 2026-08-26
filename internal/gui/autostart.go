// autostart.go has no "gui" build tag and imports no GTK, on purpose: the
// whole of the autostart decision — where the file goes, what it contains,
// whether it is on — is testable without a display. tray.go only calls into
// it.
package gui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// autostartFileName is the desktop entry the XDG autostart spec looks for.
// It deliberately matches the menu entry's basename so that a user browsing
// ~/.config/autostart recognises which program put it there.
const autostartFileName = "proxy-helper-gui.desktop"

// autostartPath is where the per-user autostart entry lives, honouring
// XDG_CONFIG_HOME the same way proxy.ConfigFilePath does — tests point it at
// a temporary directory, and a user with a relocated config dir gets their
// entry there instead of a stray one under a hardcoded ~/.config.
func autostartPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", autostartFileName), nil
}

// autostartDesktop renders the entry's contents for the binary at execPath.
//
// execPath comes from os.Executable() rather than a hardcoded /usr/bin path
// so that autostart launches the binary the user actually ran — the .deb's
// copy, a local build, or one dropped in ~/.local/bin — instead of silently
// starting a different one, or nothing at all when the package is not
// installed.
//
// The window starts hidden: something that puts itself in the tray at every
// login should not also throw a window in the user's face while they are
// logging in. That is what --hidden is for.
func autostartDesktop(execPath string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=proxy-helper
Comment=Gerencia as configurações de proxy do sistema
Exec=%s gui --hidden
Icon=proxy-helper
Terminal=false
Categories=Network;Settings;
X-GNOME-Autostart-enabled=true
`, execPath)
}

// autostartEnabled reports whether the entry exists. Presence is the whole
// signal: the XDG spec has no "installed but off" state, and inventing one
// (a key inside the file) would disagree with what every desktop's own
// startup-applications UI shows.
func autostartEnabled(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// setAutostart creates or removes the entry. Turning it off when it is
// already off is not an error — a user who deleted the file through their
// desktop's own settings must not get a failure here.
func setAutostart(path, execPath string, on bool) error {
	if !on {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if strings.TrimSpace(execPath) == "" {
		return fmt.Errorf("cannot enable autostart: the running binary's path is unknown")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(autostartDesktop(execPath)), 0o644)
}

// desktopWMClass is the program name GTK reports to the window manager, and
// the value packaging/proxy-helper-gui.desktop declares as StartupWMClass.
// The two must stay identical: the desktop file is how the launcher finds
// the running window, and a mismatch shows up as a duplicate, unnamed entry
// in the taskbar rather than as an error.
const desktopWMClass = "proxy-helper-gui"

// appID is the GApplication identifier. It is what makes a second launch
// raise the running window instead of starting another copy: GApplication
// claims this name on the session bus and routes later launches to whoever
// holds it.
//
// It must be a valid D-Bus name — at least one dot, no leading digit in an
// element — which is why it is not simply "proxy-helper-gui"; that spelling
// is rejected outright (verified with glib.ApplicationIDIsValid). It is
// deliberately distinct from desktopWMClass: this one is a bus name, that
// one is what the window manager sees, and they answer different questions.
const appID = "br.com.simonetti.ProxyHelper"
