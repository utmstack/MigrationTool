//go:build linux

package migration

import (
	"os"
)

// setExecutable sets the executable permission on a file (Linux only)
func setExecutable(path string) error {
	return os.Chmod(path, 0755)
}
