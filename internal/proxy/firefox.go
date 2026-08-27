package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FirefoxTargetName is the name accepted in --targets.
const FirefoxTargetName = "firefox"

// firefoxSystemProxy is Firefox's value for "use the system proxy settings".
// Firefox keeps its own proxy configuration and ignores the system's unless
// told otherwise, which is why a machine can have every other program going
// through the proxy while Firefox talks to the corporate proxy directly and
// asks the person for a password it should never have seen.
const firefoxSystemProxy = 5

// firefoxBaseDir resolves Firefox's data directory. It is a package
// variable so tests can point it at a temporary tree: the real one is a
// per-platform path, and the logic around it is worth testing on the
// machine this project is developed on.
var firefoxBaseDir = defaultFirefoxBaseDir

// firefoxProfilesDir returns where Firefox keeps its profiles, and whether
// that directory exists.
//
// Split out because it is the whole of this target's environment detection,
// and a machine without Firefox must skip the target rather than fail.
func firefoxProfilesDir() (string, bool) {
	base, err := firefoxBaseDir()
	if err != nil {
		return "", false
	}
	dir := filepath.Join(base, "Profiles")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// firefoxProfiles lists the profile directories, sorted so that a dry run
// prints them in a stable order.
//
// Every profile is configured, not just the default one: a person with two
// profiles uses both, and the one left out would be the one that breaks.
func firefoxProfiles() ([]string, error) {
	dir, ok := firefoxProfilesDir()
	if !ok {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// FirefoxTarget points Firefox at the system proxy settings.
//
// It sets network.proxy.type rather than the proxy host and port. The
// address belongs in one place — the Windows settings the other targets
// already write — and duplicating it here would leave two copies to drift
// apart. What Firefox needs is to stop ignoring it.
type FirefoxTarget struct{}

// NewFirefoxTarget builds the Firefox target.
func NewFirefoxTarget() *FirefoxTarget { return &FirefoxTarget{} }

func (t *FirefoxTarget) Name() string { return FirefoxTargetName }

// RequiresRoot is false: user.js lives in the person's own profile.
func (t *FirefoxTarget) RequiresRoot() bool { return false }

// SessionScoped is true: the profiles belong to the invoking user.
func (t *FirefoxTarget) SessionScoped() bool { return true }

// Available reports whether this machine has Firefox profiles to configure.
func (t *FirefoxTarget) Available() bool {
	_, ok := firefoxProfilesDir()
	return ok
}

func (t *FirefoxTarget) Set(ex *Executor, cfg Config) error {
	profiles, err := firefoxProfiles()
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		path := filepath.Join(profile, "user.js")

		// Read what the profile had before touching it. Firefox never
		// reverts a preference on its own — a value applied through user.js
		// stays written in prefs.js after the line is gone — so if we do
		// not record the previous setting now, Unset has nothing to put
		// back and the machine keeps our value forever.
		previous := currentProxyType(profile)

		content, err := upsertBlockWith(path, firefoxBody(firefoxSystemProxy, previous), slashMarkers)
		if err != nil {
			return err
		}
		if err := ex.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// firefoxBody renders the managed block: the setting, plus a note of what
// the profile had before, so Unset can restore it.
func firefoxBody(proxyType int, previous string) string {
	body := fmt.Sprintf("user_pref(\"network.proxy.type\", %d);", proxyType)
	if previous != "" {
		body += "\n" + previousMarker + previous
	}
	return body
}

// previousMarker prefixes the recorded original value inside the block. It
// is a comment so Firefox ignores it, and it lives in the block itself
// rather than in a file of ours: one thing to write, one thing to remove,
// and nothing left behind in a profile we do not own.
const previousMarker = "// proxy-helper: valor anterior = "

// currentProxyType reports the profile's network.proxy.type before we touch
// it, or "" when the profile never set one.
//
// prefs.js is the file Firefox writes itself; user.js is what an
// administrator or we put there. Both are consulted, user.js last, because
// that is the order Firefox applies them.
func currentProxyType(profile string) string {
	for _, name := range []string{"prefs.js", "user.js"} {
		if v, ok := readProxyType(filepath.Join(profile, name)); ok {
			return v
		}
	}
	return ""
}

// readProxyType pulls network.proxy.type out of a Firefox preferences file.
func readProxyType(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		// Our own block must never be read back as the original value:
		// after one Set, that would record 5 as "what it was before".
		if strings.HasPrefix(line, "//") {
			continue
		}
		const prefix = `user_pref("network.proxy.type",`
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, prefix)), ");")
		if value = strings.TrimSpace(value); value != "" {
			return value, true
		}
	}
	return "", false
}

// recordedPrevious extracts the value Set stored in the block.
func recordedPrevious(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, previousMarker) {
			return strings.TrimSpace(strings.TrimPrefix(line, previousMarker))
		}
	}
	return ""
}

func (t *FirefoxTarget) Unset(ex *Executor) error {
	profiles, err := firefoxProfiles()
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		path := filepath.Join(profile, "user.js")

		body, found, err := readBlockWith(path, slashMarkers)
		if err != nil {
			return err
		}
		if !found {
			continue
		}

		// A profile that had a proxy of its own gets it back, written
		// explicitly. Simply deleting our line would not restore anything:
		// Firefox would keep the value we applied, and whoever set that
		// proxy — usually the person's own IT — would find it gone with no
		// trace of what happened.
		if previous := recordedPrevious(body); previous != "" && previous != strconv.Itoa(firefoxSystemProxy) {
			restored, err := upsertBlockWith(path, restoreBody(previous), slashMarkers)
			if err != nil {
				return err
			}
			if err := ex.WriteFile(path, restored, 0o600); err != nil {
				return err
			}
			continue
		}

		content, _, err := removeBlockWith(path, slashMarkers)
		if err != nil {
			return err
		}
		// An emptied user.js is removed rather than left behind: Firefox
		// treats the file's presence as meaningful, and an empty one we
		// created is clutter in someone else's profile.
		if strings.TrimSpace(string(content)) == "" {
			if err := ex.RemoveFile(path); err != nil {
				return err
			}
			continue
		}
		if err := ex.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// restoreBody is the block left behind after Unset on a profile that had
// its own proxy setting.
//
// The block stays rather than being deleted, and that is the point: the
// value only takes effect because user.js reapplies it at every start. A
// deleted line would leave Firefox on whatever we last applied.
func restoreBody(previous string) string {
	return "user_pref(\"network.proxy.type\", " + previous + ");"
}

func (t *FirefoxTarget) Status(ex *Executor, elevate bool) (Status, error) {
	st := Status{Name: t.Name(), Available: true}

	profiles, err := firefoxProfiles()
	if err != nil {
		return st, err
	}
	var configured int
	for _, profile := range profiles {
		body, found, err := readBlockWith(filepath.Join(profile, "user.js"), slashMarkers)
		if err != nil {
			return st, err
		}
		if found && strings.Contains(body, previousMarker) {
			configured++
		}
	}
	if configured == 0 {
		st.Detail = "not following the system proxy settings"
		return st, nil
	}
	st.Enabled = true
	st.Detail = firefoxStatusDetail(configured, len(profiles))
	return st, nil
}

// firefoxStatusDetail words the count. A partial result is worth spelling
// out: it means one profile still talks to the proxy directly.
func firefoxStatusDetail(configured, total int) string {
	if configured == total {
		return "following the system proxy settings (restart Firefox to apply)"
	}
	return fmt.Sprintf("following the system proxy settings in %d of %d profiles (restart Firefox to apply)", configured, total)
}
