// modeview.go has no "gui" build tag and imports no GTK/glib, for the same
// reason as daemonview.go: it holds the header's mode logic — the mapping
// between the three routing modes and the two widgets that show them — so
// `go test ./...` exercises it without a graphical session.
package gui

import "proxy-helper/internal/proxy"

// modeSwitchState maps a routing mode onto the header's two controls: the
// "automático" lock and the on/off switch.
//
// Three modes do not fit one switch, and the lock is what closes the gap.
// With it on, the switch stops being an input and becomes a readout of what
// the daemon is really doing — in auto that is the mode AND the probe
// verdict together, which is precisely the pair a caller reading config.json
// alone would get wrong.
//
// reachable comes from the daemon's published runtime state, not from a
// second probe of our own: a status display that disagreed with the routing
// actually in force would be worse than no display.
func modeSwitchState(m proxy.Mode, reachable bool) (lock, on bool) {
	switch m {
	case proxy.ModeAuto:
		return true, reachable
	case proxy.ModeUpstream:
		return false, true
	default:
		return false, false
	}
}

// modeFromGesture is the inverse: what the user meant by leaving the
// controls in this position.
//
// Turning the lock off freezes whatever was in force rather than jumping to
// a default — the machine keeps doing what it was already doing, and the
// gesture reads as "stop deciding for me", not "change what you are doing".
func modeFromGesture(lock, on bool) proxy.Mode {
	if lock {
		return proxy.ModeAuto
	}
	if on {
		return proxy.ModeUpstream
	}
	return proxy.ModeDirect
}

// modeLabel is the header's wording. It is the single place mapping state to
// text, so the label and the switch position can never contradict each
// other — same rule the old masterLabel carried.
//
// A degraded auto is deliberately not worded as plain "Direto": the user
// needs to see that the machine chose direct because the upstream stopped
// answering, not that they left it switched off.
func modeLabel(m proxy.Mode, reachable bool) string {
	switch m {
	case proxy.ModeAuto:
		if reachable {
			return "Auto (ativo)"
		}
		return "Auto (direto)"
	case proxy.ModeUpstream:
		return "Ativo"
	default:
		return "Direto"
	}
}

// modeTooltip explains the state, and above all why the switch refuses to
// move: a read-only control with no explanation reads as a broken one.
func modeTooltip(m proxy.Mode, reachable, stranded bool) string {
	if stranded {
		return "O proxy local não está respondendo, e todos os alvos apontam para ele — " +
			"esta máquina está sem acesso à rede. Veja a aba Status."
	}
	switch m {
	case proxy.ModeAuto:
		if reachable {
			return "Automático: encaminhando pelo proxy, que está respondendo. " +
				"Desligue o automático para fixar um modo."
		}
		return "Automático: o proxy parou de responder, então o tráfego está indo direto. " +
			"Desligue o automático para fixar um modo."
	case proxy.ModeUpstream:
		return "Sempre encaminhando pelo proxy, mesmo que ele pare de responder."
	default:
		return "Tudo indo direto, sem passar pelo proxy."
	}
}
