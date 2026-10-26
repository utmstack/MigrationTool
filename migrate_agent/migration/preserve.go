package migration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
)

// extractForForwarder copies the forwarder-owned files out of the V11 agent
// dir into MigrationPath() before the dir is wiped. They stay there for the
// forwarder install step; the V12 agent itself never uses them.
func extractForForwarder() error {
	entries, err := os.ReadDir(config.AgentPath())
	if err != nil {
		return fmt.Errorf("read agent dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !slices.Contains(config.PreservedForForwarder, e.Name()) {
			continue
		}
		src := filepath.Join(config.AgentPath(), e.Name())
		dst := filepath.Join(config.MigrationPath(), e.Name())
		if err := copyFile(src, dst); err != nil && utils.Logger != nil {
			utils.Logger.ErrorF("Extract %s: %v", e.Name(), err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// runAgentInstall runs the v12 agent's install subcommand, which registers
// the agent against the panel and starts the service.
func runAgentInstall(binaryPath, server, key, skip string) error {
	cmd := exec.Command(binaryPath, "install", server, key, skip)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if utils.Logger != nil {
		utils.Logger.Info("Install output: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
