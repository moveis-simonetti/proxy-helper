//go:build windows

package serve

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProtectPassword encrypts a password with DPAPI, bound to the current user
// account, and returns it base64-encoded so it can live in config.json.
//
// This is what keeps the password out of plaintext on a machine where the
// app is deployed to many people at once: the ciphertext is useless to
// another account, and useless on another machine.
func ProtectPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: (*byte)(unsafe.Pointer(unsafe.StringData(plain)))}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("protecting the password: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	blob := unsafe.Slice(out.Data, out.Size)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// unprotectPassword reverses ProtectPassword.
func unprotectPassword(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("the stored password is corrupt: %w", err)
	}
	if len(raw) == 0 {
		return "", nil
	}

	in := windows.DataBlob{Size: uint32(len(raw)), Data: &raw[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		// The common causes are a different Windows account or a restored
		// profile from another machine — say that instead of surfacing a
		// Windows error code to someone who cannot act on it.
		return "", fmt.Errorf("the stored password could not be read on this account; enter it again: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	return string(unsafe.Slice(out.Data, out.Size)), nil
}
