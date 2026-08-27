package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartPathHonoursXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-probe")
	got, err := autostartPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/tmp/xdg-probe", "autostart", autostartFileName)
	if got != want {
		t.Errorf("autostartPath() = %q, want %q", got, want)
	}
}

// The entry must launch the binary that wrote it, not a hardcoded path: a
// local build or one in ~/.local/bin would otherwise autostart a different
// program, or nothing.
func TestAutostartDesktopExecsTheGivenBinary(t *testing.T) {
	got := autostartDesktop("/home/alice/.local/bin/proxy-helper-gui")
	if !strings.Contains(got, "Exec=/home/alice/.local/bin/proxy-helper-gui gui") {
		t.Errorf("Exec errado em:\n%s", got)
	}
}

// Logging in should not throw a window at the user; the point of autostart
// here is the tray icon.
func TestAutostartDesktopStartsHidden(t *testing.T) {
	if !strings.Contains(autostartDesktop("/usr/bin/proxy-helper-gui"), "--hidden") {
		t.Error("a entrada de autostart não passa --hidden")
	}
}

func TestSetAutostartCreatesAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autostart", autostartFileName)

	if autostartEnabled(path) {
		t.Fatal("autostartEnabled = true antes de criar")
	}
	if err := setAutostart(path, "/usr/bin/proxy-helper-gui", true); err != nil {
		t.Fatalf("setAutostart(on): %v", err)
	}
	if !autostartEnabled(path) {
		t.Error("autostartEnabled = false depois de criar")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "[Desktop Entry]") {
		t.Errorf("arquivo não é um desktop entry:\n%s", body)
	}

	if err := setAutostart(path, "", false); err != nil {
		t.Fatalf("setAutostart(off): %v", err)
	}
	if autostartEnabled(path) {
		t.Error("autostartEnabled = true depois de remover")
	}
}

// Someone who removed the entry through their desktop's own
// startup-applications window must not get an error from us afterwards.
func TestSetAutostartOffIsNotAnErrorWhenAlreadyOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autostart", autostartFileName)
	if err := setAutostart(path, "", false); err != nil {
		t.Errorf("setAutostart(off) num arquivo inexistente: %v", err)
	}
}

func TestSetAutostartOnRefusesAnUnknownBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autostart", autostartFileName)
	if err := setAutostart(path, "  ", true); err == nil {
		t.Error("setAutostart aceitou um caminho vazio; a entrada não iniciaria nada")
	}
	if autostartEnabled(path) {
		t.Error("escreveu a entrada mesmo assim")
	}
}

// The desktop entry and the code must agree on the program name. GTK derives
// WM_CLASS from it (verified with xprop: a binary named "ph-gui" produced
// WM_CLASS ("ph-gui","Ph-gui")), and StartupWMClass is how the launcher
// finds the running window. A mismatch is not an error anywhere — it just
// shows up as a duplicate, unnamed taskbar entry, which is exactly the kind
// of drift nothing else would catch.
func TestDesktopEntryMatchesTheProgramName(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "packaging", "proxy-helper-gui.desktop"))
	if err != nil {
		t.Fatalf("lendo o desktop entry: %v", err)
	}
	want := "StartupWMClass=" + desktopWMClass
	if !strings.Contains(string(body), want) {
		t.Errorf("packaging/proxy-helper-gui.desktop não contém %q", want)
	}
	// The menu entry launches the installed binary by name; the autostart
	// entry uses an absolute path instead (os.Executable), so only this one
	// depends on PATH.
	if !strings.Contains(string(body), "Exec="+desktopWMClass+" gui") {
		t.Errorf("Exec não invoca %q", desktopWMClass+" gui")
	}
}

// Icon= must name an icon the package actually installs. Like
// StartupWMClass, a wrong value fails silently — the entry just shows a
// generic placeholder — so nothing but a check like this catches it.
func TestDesktopEntryUsesTheInstalledIconName(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "packaging", "proxy-helper-gui.desktop"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Icon="+appIconName+"\n") {
		t.Errorf("desktop entry não usa Icon=%s", appIconName)
	}
	for _, name := range []string{appIconName + ".svg", trayIconOn + ".svg", trayIconOff + ".svg"} {
		if _, err := os.Stat(filepath.Join("..", "..", "packaging", "icons", name)); err != nil {
			t.Errorf("packaging/icons/%s não existe: %v", name, err)
		}
	}
}

// The autostart entry is written at runtime, so its icon name cannot be
// checked by reading a file in the repo — it has to come from the same
// constant the package installs under.
func TestAutostartDesktopUsesTheInstalledIconName(t *testing.T) {
	if !strings.Contains(autostartDesktop("/usr/bin/proxy-helper-gui"), "Icon="+appIconName+"\n") {
		t.Errorf("a entrada de autostart não usa Icon=%s", appIconName)
	}
}

// iconSniffWindow is how far into a file the icon must declare itself. The
// real limit measured on gdk-pixbuf 2.42.12 / librsvg 2.60.0 / GTK 3.24.50
// was around 256 bytes; this is deliberately far stricter, because the
// number is an implementation detail of the format sniffing and could
// tighten without warning.
const iconSniffWindow = 128

// The icons were once shipped with an explanatory comment header, and all
// three became invisible: the tray icon never appeared in the panel and the
// GNOME menu entry showed an empty tile. gdk-pixbuf sniffs only a prefix of
// the file to recognise the format, and the header pushed "<svg" past it.
//
// The failure mode is why this test exists rather than a code review note:
// IconTheme.HasIcon() still returns true — the theme knows the file, nothing
// can draw it — so every check short of actually rendering says the icons
// are fine. Rationale for these files lives in packaging/icons/README.md,
// which is not parsed by anything.
func TestIconsPutTheSvgTagFirst(t *testing.T) {
	for _, name := range []string{appIconName + ".svg", trayIconOn + ".svg", trayIconOff + ".svg"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "packaging", "icons", name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		at := strings.Index(string(body), "<svg")
		if at < 0 {
			t.Errorf("%s: não tem tag <svg>", name)
			continue
		}
		if at > iconSniffWindow {
			t.Errorf("%s: <svg> começa no byte %d, além dos %d que o gdk-pixbuf fareja — "+
				"o ícone vai falhar em silêncio (veja packaging/icons/README.md)", name, at, iconSniffWindow)
		}
	}
}

// The application ID has to be a valid D-Bus name or GApplication refuses it
// and single-instance handling silently never engages — the symptom being the
// bug this replaced: clicking the menu entry while the window sits in the tray
// starts a whole second process. "proxy-helper-gui" is NOT valid (no dot),
// which is why appID and desktopWMClass are deliberately different strings.
func TestAppIDIsAValidBusName(t *testing.T) {
	if !strings.Contains(appID, ".") {
		t.Errorf("appID = %q, precisa de ao menos um ponto para ser um nome de barramento válido", appID)
	}
	if strings.Contains(appID, "-") {
		t.Errorf("appID = %q, hífen é arriscado num nome de barramento", appID)
	}
	if appID == desktopWMClass {
		t.Error("appID e desktopWMClass são coisas diferentes: um é nome de barramento, o outro é o que o gerenciador de janelas vê")
	}
}
