//go:build windows

package proxy

import (
	"os"
	"path/filepath"
)

// defaultFirefoxBaseDir is %AppData%\Mozilla\Firefox on Windows.
func defaultFirefoxBaseDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Mozilla", "Firefox"), nil
}
