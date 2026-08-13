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
	Available() bool
	Set(ex *Executor, cfg Config) error
	Unset(ex *Executor) error
	// Status reports current state. elevate requests a privileged read
	// (via sudo) for targets whose state isn't readable as a normal user
	// (e.g. `snap get system ...`); targets that don't need it ignore it.
	Status(elevate bool) (Status, error)
}
