package migration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
)

// extractForForwarder copies the forwarder-owned files out of the V11 agent
// dir into MigrationPath() before the dir is wiped. They stay there for the
// forwarder install step; the V12 agent itself never uses them.
func extractForForwarder() error {
	for _, rel := range config.PreservedForForwarder {
		src := filepath.Join(config.AgentPath(), rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(config.MigrationPath(), rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			if utils.Logger != nil {
				utils.Logger.ErrorF("Extract dir %s: %v", filepath.Dir(dst), err)
			}
			continue
		}
		if err := copyFile(src, dst); err != nil && utils.Logger != nil {
			utils.Logger.ErrorF("Extract %s: %v", rel, err)
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
