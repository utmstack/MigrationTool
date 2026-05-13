//go:build darwin

package migration

import (
	"os"
)

// setExecutable sets the executable permission on a file (same as Linux)
func setExecutable(path string) error {
	return os.Chmod(path, 0755)
}
