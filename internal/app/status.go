package app

import (
	"fmt"

	"proxy-helper/internal/proxy"
)

// Collect reports the current state of each named target, in registry order.
// A target whose Status call fails is reported as unavailable with the error
// in Detail rather than aborting the sweep: one broken target must not hide
// the other ten.
func Collect(d Deps, ex *proxy.Executor, targetNames []string, elevate bool) ([]proxy.Status, error) {
	targets, err := d.ResolveTargets(targetNames)
	if err != nil {
		return nil, err
	}

	out := make([]proxy.Status, 0, len(targets))
	for _, t := range targets {
		st, err := t.Status(ex, elevate)
		if err != nil {
			out = append(out, proxy.Status{Name: t.Name(), Detail: fmt.Sprintf("error: %v", err)})
			continue
		}
		st.Name = t.Name()
		out = append(out, st)
	}
	return out, nil
}

// NeedsElevation reports whether any target could not be read as this user,
// so the caller can offer to retry with sudo exactly once.
func NeedsElevation(sts []proxy.Status) bool {
	for _, st := range sts {
		if st.NeedsElevation {
			return true
		}
	}
	return false
}

// Elevate re-reads only the targets that could not be read as this user,
// leaving every other result untouched. Callers offer this once, after
// asking the user, so targets that already answered are not queried again.
func Elevate(d Deps, ex *proxy.Executor, sts []proxy.Status) ([]proxy.Status, error) {
	var names []string
	for _, st := range sts {
		if st.NeedsElevation {
			names = append(names, st.Name)
		}
	}
	if len(names) == 0 {
		return sts, nil
	}

	targets, err := d.ResolveTargets(names)
	if err != nil {
		return nil, err
	}

	rereads := make(map[string]proxy.Status, len(targets))
	for _, t := range targets {
		st, err := t.Status(ex, true)
		if err != nil {
			rereads[t.Name()] = proxy.Status{Name: t.Name(), Detail: fmt.Sprintf("error: %v", err)}
			continue
		}
		st.Name = t.Name()
		rereads[t.Name()] = st
	}

	out := make([]proxy.Status, len(sts))
	for i, st := range sts {
		if re, ok := rereads[st.Name]; ok {
			out[i] = re
			continue
		}
		out[i] = st
	}
	return out, nil
}
