//go:build !windows

package serve

import "proxy-helper/internal/proxy"

// StorePassword puts pass into cfg in the safest form this platform offers.
//
// There is no DPAPI here, so this keeps the existing Linux behaviour: the
// password lives in config.json, which profiles.go writes with mode 0600.
// The Windows build is the one that needed better, because it is deployed
// to many machines at once by someone who is not the person using it.
func StorePassword(cfg *proxy.Config, pass string) error {
	cfg.Password = pass
	cfg.PasswordProtected = ""
	return nil
}
