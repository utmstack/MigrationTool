//go:build linux

package cmd

import (
	"fmt"
	"os"
)

// detachFromUnitCgroup moves this process into init.scope so the unit stop
// (SIGTERM to the whole unit cgroup) does not kill the child before
// Uninstall() and the directory cleanup run. Best-effort: if the cgroup v2
// path is unavailable the old behavior (leftover unit file) applies.
func detachFromUnitCgroup() {
	f, err := os.OpenFile("/sys/fs/cgroup/init.scope/cgroup.procs", os.O_WRONLY, 0)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "%d", os.Getpid())
	_ = f.Close()
}
