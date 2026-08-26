// trayicon.go has no "gui" build tag: which icon name the tray asks for is a
// decision, and a decision is testable without a display.
package gui

// appIconName is the icon theme name for the application icon — the window,
// the task bar, the menu entry. It is the product name, NOT the binary name
// (proxy-helper-gui, which is desktopWMClass): the two are different things,
// and conflating them silently breaks whichever one is wrong, since a
// missing icon name never raises an error.
const appIconName = "proxy-helper"

// The tray's own icons, installed by the .deb under
// /usr/share/icons/hicolor/symbolic/apps. Two variants so the panel shows
// whether the proxy is on without the user opening anything — half the point
// of having a tray icon at all.
const (
	trayIconOn  = "proxy-helper-symbolic"
	trayIconOff = "proxy-helper-off-symbolic"
)

// trayIconFallback is a stock name from adwaita-icon-theme, which GTK3
// already depends on. It exists because an icon name that is not in the
// running theme fails SILENTLY — no icon, no error, no log line. A local
// build, or a binary copied somewhere without installing the package's
// icons, would otherwise put an invisible item in the panel.
const trayIconFallback = "network-workgroup-symbolic"

// trayIconFor picks the tray icon name. installed reports whether our own
// icons are present in the running icon theme (tray.go asks GTK); on is the
// proxy's state.
//
// The fallback deliberately does NOT vary with state: a single stock icon
// that is visibly not ours is honest about being a stand-in, whereas picking
// two unrelated stock icons would invent a state signal out of shapes that
// were never drawn to carry one.
func trayIconFor(installed, on bool) string {
	if !installed {
		return trayIconFallback
	}
	if on {
		return trayIconOn
	}
	return trayIconOff
}
