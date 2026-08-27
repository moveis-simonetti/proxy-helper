package serve

import (
	"fmt"
	"os"
	"strings"

	"proxy-helper/internal/proxy"
)

// GlobalPasswordEnv is consulted when a profile names no password source of
// its own.
const GlobalPasswordEnv = "PROXY_HELPER_PASSWORD"

// Resolve returns the credentials the daemon should present upstream.
//
// Sources are tried in order: the profile's password_file, the profile's
// password_env, the global PROXY_HELPER_PASSWORD, and finally the legacy
// plaintext "pass" field. deprecated is true only for that last case, so the
// caller can warn once at startup instead of on every request.
//
// A missing password is not an error: plenty of proxies need no auth.
func Resolve(cfg proxy.Config) (user, pass string, deprecated bool, err error) {
	user = cfg.Username

	if cfg.PasswordFile != "" {
		info, statErr := os.Stat(cfg.PasswordFile)
		if statErr != nil {
			return user, "", false, fmt.Errorf("reading password_file: %w", statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return user, "", false, fmt.Errorf(
				"password_file %s is readable by group or others (mode %04o); run: chmod 600 %s",
				cfg.PasswordFile, info.Mode().Perm(), cfg.PasswordFile)
		}
		content, readErr := os.ReadFile(cfg.PasswordFile)
		if readErr != nil {
			return user, "", false, fmt.Errorf("reading password_file: %w", readErr)
		}
		return user, strings.TrimRight(string(content), "\r\n"), false, nil
	}

	if cfg.PasswordEnv != "" {
		if v := os.Getenv(cfg.PasswordEnv); v != "" {
			return user, v, false, nil
		}
	}

	if v := os.Getenv(GlobalPasswordEnv); v != "" {
		return user, v, false, nil
	}

	if cfg.Password != "" {
		return user, cfg.Password, true, nil
	}
	return user, "", false, nil
}
