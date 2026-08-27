//go:build gui

package gui

// #cgo pkg-config: ayatana-appindicator3-0.1
// #include <libayatana-appindicator/app-indicator.h>
//
// static AppIndicator *new_indicator(const char *id, const char *icon_name) {
//   return app_indicator_new(id, icon_name, APP_INDICATOR_CATEGORY_APPLICATION_STATUS);
// }
//
// static void indicator_set_active(AppIndicator *indicator) {
//   app_indicator_set_status(indicator, APP_INDICATOR_STATUS_ACTIVE);
// }
//
// static void indicator_set_menu(AppIndicator *indicator, GtkWidget *menu) {
//   app_indicator_set_menu(indicator, GTK_MENU(menu));
// }
//
// static void indicator_set_icon(AppIndicator *indicator, const char *icon_name) {
//   app_indicator_set_icon_full(indicator, icon_name, "proxy-helper");
// }
import "C"

import (
	"fmt"
	"os"
	"unsafe"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/gotk3/gotk3/gtk"
)

// ownIconsInstalled reports whether the package's own icons are in the
// running icon theme. It is a question only GTK can answer, and the answer
// matters: an icon name absent from the theme fails silently — no icon, no
// error — so a build running without the .deb's icons installed would put an
// invisible item in the panel. trayIconFor (trayicon.go) turns this into the
// name to use.
func ownIconsInstalled() bool {
	theme, err := gtk.IconThemeGetDefault()
	if err != nil {
		return false
	}
	return theme.HasIcon(trayIconOn) && theme.HasIcon(trayIconOff)
}

// tray owns the AppIndicator and its menu. It is nil when the desktop has no
// tray — no indicator library, no extension to render it, or the indicator
// call itself failed — and every method here is safe on that nil receiver
// so the rest of the GUI never needs an availability check before calling
// into it. window's delete-event handler relies on exactly that: it always
// calls t.hasIndicator() rather than checking t == nil itself.
type tray struct {
	indicator     *C.AppIndicator
	menu          *gtk.Menu
	closeItem     *gtk.CheckMenuItem
	autostartItem *gtk.CheckMenuItem

	// win and r are what the toggle and profile-switch menu actions need:
	// win to Present() the window (routeRewritesTargets refusal, and the
	// existing "Abrir" item), r to run app.On/Off/Enable off the GTK
	// thread, same as headerbar.go.
	win *window
	r   *runner

	toggleItem  *gtk.MenuItem
	profileItem *gtk.MenuItem
	profileMenu *gtk.Menu

	// active mirrors config.json's "there is an active profile" fact, as
	// of the last refresh() delivery. It is only ever read/written on the
	// GTK thread (toggleItem's handler reads it when queuing a job;
	// applyProfiles, itself always a runner delivery, writes it), so it
	// needs no lock despite being read from inside a submitted job's
	// closure-capture.
	active bool

	// onChanged is wired by app.go once the headerbar exists — after
	// newTray returns, since app.go builds the tray before the headerbar
	// (the tray's own "Abrir" item does not need it). It is invoked after
	// a successful toggle or profile switch triggered from this menu, to
	// keep the headerbar selector and the Status/Daemon pages in sync with
	// whatever the tray just did — the same job onProfileChanged does for
	// the headerbar's own switch and master toggle. This is never called
	// during construction, only from user-triggered menu activations that
	// can only happen after Run has finished wiring everything, so a nil
	// check here is defensive rather than load-bearing.
	onChanged func()
}

// newTray builds the AppIndicator and its menu. It returns (nil, nil) when
// no indicator could be created — that is an environment fact (no tray on
// this desktop), not an error: a desktop without a tray is a supported
// place to run this program, and callers must treat a nil tray as normal,
// not as something to report.
//
// win.Present raises and shows the window from the "Abrir proxy-helper" menu
// item; r is used to persist the close_to_tray preference (via r.submit, off
// the GTK thread) when the "Fechar esconde na bandeja" item is toggled.
func newTray(win *window, r *runner, closeToTray bool) (*tray, error) {
	idC := C.CString("proxy-helper")
	defer C.free(unsafe.Pointer(idC))
	iconC := C.CString(trayIconFor(ownIconsInstalled(), false))
	defer C.free(unsafe.Pointer(iconC))

	indicator := C.new_indicator(idC, iconC)
	if indicator == nil {
		return nil, nil
	}

	menu, err := gtk.MenuNew()
	if err != nil {
		return nil, err
	}

	openItem, err := gtk.MenuItemNewWithLabel("Abrir proxy-helper")
	if err != nil {
		return nil, err
	}
	openItem.Connect("activate", func() {
		win.Window.Present()
	})
	menu.Append(openItem)

	sep1, err := gtk.SeparatorMenuItemNew()
	if err != nil {
		return nil, err
	}
	menu.Append(sep1)

	t := &tray{indicator: indicator, menu: menu, win: win, r: r}

	toggleItem, err := gtk.MenuItemNewWithLabel("Ativar proxy")
	if err != nil {
		return nil, err
	}
	toggleItem.Connect("activate", func() { t.tryToggle() })
	menu.Append(toggleItem)
	t.toggleItem = toggleItem

	profileItem, err := gtk.MenuItemNewWithLabel("Perfil")
	if err != nil {
		return nil, err
	}
	profileMenu, err := gtk.MenuNew()
	if err != nil {
		return nil, err
	}
	profileItem.SetSubmenu(profileMenu)
	menu.Append(profileItem)
	t.profileItem = profileItem
	t.profileMenu = profileMenu

	sep2, err := gtk.SeparatorMenuItemNew()
	if err != nil {
		return nil, err
	}
	menu.Append(sep2)

	closeItem, err := gtk.CheckMenuItemNewWithLabel("Fechar esconde na bandeja")
	if err != nil {
		return nil, err
	}
	closeItem.SetActive(closeToTray)
	closeItem.Connect("toggled", func() {
		active := closeItem.GetActive()
		r.submit(func() func() {
			// I/O off the GTK thread, per package convention: writing
			// config.json here directly (instead of inside this submitted
			// job) would run flock/file I/O on the UI thread.
			_ = proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
				pf.CloseToTray = active
				return nil
			})
			return nil
		})
	})
	menu.Append(closeItem)
	t.closeItem = closeItem

	// "Iniciar com o sistema" writes a per-user XDG autostart entry rather
	// than anything the package installs, so it stays each user's choice
	// instead of the package deciding for everyone. Presence of the file IS
	// the state (see autostart.go), which keeps this item agreeing with the
	// desktop's own startup-applications window.
	autostartItem, err := gtk.CheckMenuItemNewWithLabel("Iniciar com o sistema")
	if err != nil {
		return nil, err
	}
	// Read before connecting the handler, so restoring the current state
	// does not fire "toggled" and rewrite what it just read.
	if path, err := autostartPath(); err == nil {
		autostartItem.SetActive(autostartEnabled(path))
	}
	autostartItem.Connect("toggled", func() {
		active := autostartItem.GetActive()
		r.submit(func() func() {
			// File I/O off the GTK thread, same convention as closeItem.
			path, err := autostartPath()
			if err == nil {
				// os.Executable, not a hardcoded /usr/bin path: autostart
				// must relaunch the binary actually running, whether that
				// is the .deb's, a local build, or one in ~/.local/bin.
				exe, exeErr := os.Executable()
				if exeErr == nil {
					err = setAutostart(path, exe, active)
				} else {
					err = exeErr
				}
			}
			if err == nil {
				return nil
			}
			return func() {
				// Put the item back where reality is: the toggle did not
				// take, and a checkbox claiming otherwise would be a lie.
				if p, pErr := autostartPath(); pErr == nil {
					autostartItem.SetActive(autostartEnabled(p))
				}
				_ = showTextDialog(win.Window, "Início automático", fmt.Sprintf("Não foi possível alterar o início automático: %s", err))
			}
		})
	})
	menu.Append(autostartItem)
	t.autostartItem = autostartItem

	quitItem, err := gtk.MenuItemNewWithLabel("Sair")
	if err != nil {
		return nil, err
	}
	quitItem.Connect("activate", func() {
		// win.quit, not gtk.MainQuit: there is no gtk.Main() loop since the
		// move to GtkApplication, and MainQuit against a loop that does not
		// exist does nothing at all — no error, no exit.
		win.quit()
	})
	menu.Append(quitItem)

	menu.ShowAll()

	// menu.GObject, not menu.Native(): Native() returns a uintptr, and
	// converting a uintptr back into an unsafe.Pointer is exactly what
	// "go vet" flags as a possible misuse — the garbage collector does not
	// track uintptrs, so the value is not guaranteed to still point at a
	// live object. GObject is already a real pointer, so this is a plain
	// pointer-to-pointer conversion. This matters concretely: CI runs
	// "go vet -tags gui ./...", so the uintptr form fails the build.
	menuWidget := (*C.GtkWidget)(unsafe.Pointer(menu.GObject))
	C.indicator_set_menu(indicator, menuWidget)
	C.indicator_set_active(indicator)

	// Paints the toggle label and profile submenu for the first time. Done
	// via refresh() (a runner job), not a direct proxy.LoadProfiles() call
	// here, to keep config.json I/O off the GTK thread consistently rather
	// than making an exception for "just this once at startup" — the
	// runner is already started by the time app.go calls newTray.
	t.refresh()

	return t, nil
}

// hasIndicator reports whether t is a live tray. Safe on a nil receiver —
// this is the one check window.go needs before deciding whether "close"
// means "hide" or "quit".
func (t *tray) hasIndicator() bool {
	return t != nil && t.indicator != nil
}

// closeToTray reports the current state of the "Fechar esconde na bandeja"
// menu item. Safe on a nil receiver; a tray-less desktop always reports
// false, which combined with hasIndicator's false is what forces
// window.go's delete-event handler down the "quit for real" path.
func (t *tray) closeToTray() bool {
	if t == nil || t.closeItem == nil {
		return false
	}
	return t.closeItem.GetActive()
}

// setOnChanged wires onChanged. A plain field assignment on win.Tray from
// app.go would panic when there is no tray (win.Tray is a nil *tray on a
// desktop with no indicator) — Go must dereference the pointer to write a
// field, even one that is never read again. This method exists so that
// wiring stays a no-op instead, matching every other tray method's
// nil-receiver safety.
func (t *tray) setOnChanged(fn func()) {
	if t == nil {
		return
	}
	t.onChanged = fn
}

// refresh re-reads config.json and repaints the toggle label and the
// profile submenu to match it. Safe on a nil receiver, and safe to call
// from the GTK thread: the read happens inside a runner job, off it. This
// is what app.go calls whenever a profile change happens through any other
// path (headerbar selector, Perfis page, Importar page), so the tray menu
// never shows a stale state/profile.
func (t *tray) refresh() {
	if t == nil {
		return
	}
	t.r.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		return func() { t.applyProfiles(pf, err) }
	})
}

// applyProfiles paints the toggle item and rebuilds the profile submenu
// from pf. Runs on the GTK thread only, from a runner delivery (see
// refresh). A load failure (missing/corrupt config.json) is treated the
// same as "no active profile, no profiles to offer": both items go
// insensitive rather than acting on stale or fabricated data.
func (t *tray) applyProfiles(pf *proxy.ProfileFile, err error) {
	if err != nil {
		t.active = false
		t.toggleItem.SetLabel("Ativar proxy")
		t.toggleItem.SetSensitive(false)
		t.setIcon(false)
		t.rebuildProfileMenu(nil, "")
		return
	}

	t.toggleItem.SetSensitive(true)
	t.active = pf.ActiveProfile != ""
	if t.active {
		t.toggleItem.SetLabel("Desativar proxy")
	} else {
		t.toggleItem.SetLabel("Ativar proxy")
	}
	t.setIcon(t.active)

	t.rebuildProfileMenu(visibleProfiles(pf), pf.ActiveProfile)
}

// rebuildProfileMenu clears the "Perfil" submenu and repopulates it with
// one RadioMenuItem per visible profile, marking active. RadioMenuItem
// (not CheckMenuItem) so the items behave as the mutually-exclusive choice
// they represent: clicking the already-active profile is then a no-op
// visually, rather than a checkbox that unchecks itself under the user's
// own click. Every item is destroyed and recreated on each call — cheap
// for the handful of profiles this menu will ever hold — because gotk3
// gives no cheaper way to relabel a GtkMenu's children in place.
func (t *tray) rebuildProfileMenu(names []string, active string) {
	t.profileMenu.GetChildren().Foreach(func(item interface{}) {
		if w, ok := item.(*gtk.Widget); ok {
			t.profileMenu.Remove(w)
		}
	})

	t.profileItem.SetSensitive(len(names) > 0)

	var group *gtk.RadioMenuItem
	for _, name := range names {
		item, err := gtk.RadioMenuItemNewWithLabelFromWidget(group, name)
		if err != nil {
			continue
		}
		group = item
		if name == active {
			item.SetActive(true)
		}
		n := name
		item.Connect("activate", func() { t.trySwitch(n) })
		t.profileMenu.Append(item)
	}

	// New items are born hidden (no-show-all does not apply to a plain
	// Append here, but a fresh GtkMenuItem still needs an explicit Show):
	// ShowAll on the submenu itself is what actually makes them visible.
	t.profileMenu.ShowAll()
}

// tryToggle runs app.On/app.Off off the GTK thread, exactly like the
// headerbar master switch (see headerbarCtl.trySetMaster): it is pure
// state plus a daemon reload, never a target, so EscalateNone is safe and
// no confirmation is needed.
func (t *tray) tryToggle() {
	wasActive := t.active
	t.r.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		var err error
		if wasActive {
			_, err = app.Off(statusDeps(), ex)
		} else {
			_, err = app.On(statusDeps(), ex, "")
		}
		return func() {
			if err != nil {
				verb := "ativar"
				if wasActive {
					verb = "desativar"
				}
				t.showError(fmt.Sprintf("erro ao %s proxy: %s", verb, err))
				return
			}
			if t.onChanged != nil {
				t.onChanged()
			}
		}
	})
}

// trySwitch is the tray menu's entry point for picking a profile. It
// reloads config.json fresh (not the profileMenu's captured state, which
// could be one refresh stale) and asks routeForSwitch which of app.Enable's
// two reachable routes this is — the same security-relevant decision
// headerbar.trySwitch makes, and for the same non-negotiable reason: a
// route-3 switch (routeRewritesTargets) rewrites every target and can pop
// a polkit password prompt, and doing that from a tray menu with the
// window possibly hidden would be a password prompt appearing out of
// nowhere, with zero context. So routeRewritesTargets is refused here: the
// window is raised instead, and the user finishes the switch through the
// headerbar selector, where the confirmation dialog already exists and is
// already tested. Only routeStateOnly proceeds directly from this menu.
func (t *tray) trySwitch(name string) {
	t.r.submit(func() func() {
		pf, err := proxy.LoadProfiles()
		return func() {
			if err != nil {
				t.showError(fmt.Sprintf("erro ao carregar perfis: %s", err))
				return
			}
			if routeForSwitch(pf.ViaLocal) != routeStateOnly {
				t.win.Window.Present()
				return
			}
			t.doSwitch(name)
		}
	})
}

// doSwitch performs the routeStateOnly switch: state only, no target
// touched, no privilege needed — mirrors headerbarCtl.doEnable's
// includePrivileged=false branch, minus the dialog/current-tracking that
// only the headerbar's own combo box needs.
func (t *tray) doSwitch(name string) {
	t.r.submit(func() func() {
		ex := &proxy.Executor{Escalation: proxy.EscalateNone}
		_, err := app.Enable(statusDeps(), ex, name, nil, false)
		return func() {
			if err != nil {
				t.showError(fmt.Sprintf("erro ao trocar de perfil: %s", err))
				return
			}
			if t.onChanged != nil {
				t.onChanged()
			}
		}
	})
}

// showError reports a failure via a modal dialog transient for the main
// window, matching headerbarCtl.showError's convention.
func (t *tray) showError(msg string) {
	dlg := gtk.MessageDialogNew(t.win.Window, gtk.DIALOG_MODAL, gtk.MESSAGE_ERROR, gtk.BUTTONS_OK, "%s", msg)
	dlg.SetModal(true)
	dlg.SetTransientFor(t.win.Window)
	dlg.Run()
	dlg.Destroy()
}

// setIcon repaints the indicator for the proxy's state. Nil-safe like every
// other method here, so callers never check for a tray-less desktop.
func (t *tray) setIcon(on bool) {
	if t == nil || t.indicator == nil {
		return
	}
	name := C.CString(trayIconFor(ownIconsInstalled(), on))
	defer C.free(unsafe.Pointer(name))
	C.indicator_set_icon(t.indicator, name)
}
