package proxy

// Status reports the current proxy state for a single target.
type Status struct {
	Name      string
	Available bool
	Enabled   bool
	Detail    string
	// NeedsElevation is set when a non-privileged read couldn't determine
	// the real state (e.g. snap always denies reads to non-root), so the
	// caller can offer to retry with Status(true).
	NeedsElevation bool
}

// Target is a single place proxy settings can be applied: a shell profile,
// a tool's config file, a system service, etc.
type Target interface {
	Name() string
	RequiresRoot() bool
	// SessionScoped reports whether this target talks to the invoking
	// user's desktop session (D-Bus, running desktop environment) rather
	// than to system-wide state. It answers a different question than
	// RequiresRoot: RequiresRoot says whether *this* target's write needs
	// root, while SessionScoped says whether root would even work — a
	// session-scoped target is meaningless (and typically broken, for lack
	// of $DISPLAY/session D-Bus) run as another user. A caller deciding
	// whether to elevate must check SessionScoped first and never route a
	// session-scoped target into a privileged/elevated call, regardless of
	// what RequiresRoot answers for it.
	SessionScoped() bool
	Available() bool
	Set(ex *Executor, cfg Config) error
	Unset(ex *Executor) error
	// Status reports current state. elevate requests a privileged read for
	// targets whose state isn't readable as a normal user (e.g. snap);
	// targets that don't need it ignore both arguments. ex carries the
	// escalation choice, so a GUI can forbid a prompt it cannot show.
	Status(ex *Executor, elevate bool) (Status, error)
}
