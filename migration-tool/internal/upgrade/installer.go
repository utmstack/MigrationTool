package upgrade

import (
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

func (u *Upgrader) installV11() error {
	ui.SubHeader("Phase 4: Install UTMStack V11")
	ui.Blank()

	homeDir := config.GetHomeDir(u.sourceCfg.SSHUser)
	installerPath := fmt.Sprintf("%s/installer", homeDir)

	ui.StepCompact(1, 3, "Downloading installer")
	ui.Detail("Path", installerPath)

	downloadCmd := fmt.Sprintf("sh -c 'wget -q --show-progress %s -O %s && chmod +x %s'",
		InstallerURL, installerPath, installerPath)
	output, err := u.executor.ExecuteCommandWithSudo(downloadCmd)
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to download installer: %v", err))
		return fmt.Errorf("failed to download installer: %w (output: %s)", err, output)
	}

	exists, err := u.executor.FileExists(installerPath)
	if err != nil || !exists {
		ui.Error("Installer file not found after download")
		return fmt.Errorf("installer file not found after download: %s", installerPath)
	}

	ui.Success("Installer downloaded")

	ui.StepCompact(2, 3, "Running installer")
	ui.Info("This may take 10-30 minutes")
	ui.Blank()
	ui.Message(ui.Dim("─────────────── INSTALLER OUTPUT ───────────────"))

	output, err = u.executor.ExecuteCommandWithSudoStreaming(installerPath)

	ui.Message(ui.Dim("─────────────────────────────────────────────────"))
	ui.Blank()

	if err != nil {
		ui.Error(fmt.Sprintf("Installation failed: %v", err))
		return fmt.Errorf("installer failed: %w (output: %s)", err, output)
	}
	ui.Success("Installation completed")

	return nil
}
