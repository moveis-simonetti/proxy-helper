//go:build !windows

package serve

import "fmt"

// ProtectPassword has no equivalent outside Windows. Linux keeps the
// password in a 0600 file (password_file), which Resolve already handles.
func ProtectPassword(plain string) (string, error) {
	return "", fmt.Errorf("protected passwords are a Windows feature; use password_file instead")
}

// unprotectPassword refuses rather than guessing. A config carrying
// password_protected was written on Windows and its ciphertext is bound to
// a Windows account, so there is nothing this platform could do with it —
// and silently falling through to the next password source would start the
// daemon with no credentials and fail later, at request time, far from the
// cause.
func unprotectPassword(encoded string) (string, error) {
	return "", fmt.Errorf("this profile's password was saved on Windows and cannot be read here; set password_file instead")
}
