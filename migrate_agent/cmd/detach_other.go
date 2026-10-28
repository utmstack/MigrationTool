//go:build windows || darwin

package cmd

// detachFromUnitCgroup is a no-op off Linux: Windows services and macOS
// have no unit cgroup to detach from.
func detachFromUnitCgroup() {}
