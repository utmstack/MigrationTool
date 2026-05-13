//go:build windows

package migration

// setExecutable is a no-op on Windows (executability is determined by extension)
func setExecutable(path string) error {
	return nil
}
