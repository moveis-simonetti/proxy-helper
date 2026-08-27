//go:build windows

package serve

import "proxy-helper/internal/proxy"

// StorePassword puts pass into cfg in the safest form this platform offers.
//
// On Windows that is DPAPI: the ciphertext is bound to this user account on
// this machine, so a copied config.json is useless elsewhere.
func StorePassword(cfg *proxy.Config, pass string) error {
	blob, err := ProtectPassword(pass)
	if err != nil {
		return err
	}
	cfg.PasswordProtected = blob
	// Never leave both forms behind: a stale plaintext field would keep
	// working and quietly outlive the password the person just changed.
	cfg.Password = ""
	return nil
}
