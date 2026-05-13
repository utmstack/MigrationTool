package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/service"
	"github.com/utmstack/MigrationTool/utils"
)

// VersionInfo represents the version.json structure
type VersionInfo struct {
	Version string `json:"version"`
}

// Migrator handles the v10 to v11 migration process
type Migrator struct {
	state *StateFile
	cfg   *config.AgentConfig
}

// NewMigrator creates a new Migrator instance
func NewMigrator() (*Migrator, error) {
	state, err := LoadState()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	return &Migrator{
		state: state,
	}, nil
}

// Run executes the main migration loop
func (m *Migrator) Run(ctx context.Context) error {
	if utils.Logger != nil {
		utils.Logger.Info("Migration service started, polling every %v", config.PollInterval)
	}

	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()

	// Check immediately on start
	if err := m.checkAndMigrate(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Migration check failed: %v", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			if utils.Logger != nil {
				utils.Logger.Info("Migration service shutting down")
			}
			return nil
		case <-ticker.C:
			if err := m.checkAndMigrate(); err != nil {
				if utils.Logger != nil {
					utils.Logger.ErrorF("Migration check failed: %v", err)
				}
			}
		}
	}
}

func (m *Migrator) checkAndMigrate() error {
	// If already failed permanently, don't retry
	if m.state.State == StateFailed {
		return nil
	}

	// If migration is done, don't do anything more
	if m.state.State == StateDone {
		return nil
	}

	// If we're in self-uninstall state, just set done and exit
	// (the self-uninstall process is already running or will run)
	if m.state.State == StateSelfUninstall {
		if utils.Logger != nil {
			utils.Logger.Info("Migration already completed, waiting for self-uninstall to finish")
		}
		m.state.SetState(StateDone)
		return nil
	}

	// If we were in the middle of a migration, resume from that state
	if m.state.State != StateIdle && m.state.State != StateDone {
		// Check max retries before resuming
		if m.state.RetryCount >= config.MigrationMaxRetries {
			if utils.Logger != nil {
				utils.Logger.ErrorF("Migration failed after %d attempts, giving up", m.state.RetryCount)
			}
			m.state.SetState(StateFailed)
			return fmt.Errorf("migration failed permanently after %d attempts", m.state.RetryCount)
		}
		if utils.Logger != nil {
			utils.Logger.Info("Resuming migration from state: %s (attempt %d/%d)", m.state.State, m.state.RetryCount+1, config.MigrationMaxRetries)
		}
		return m.executeMigration()
	}

	// If state is Done, don't re-detect v11
	if m.state.State == StateDone {
		return nil
	}

	// Check if v11 is detected
	version, isV11, err := m.checkVersion()
	if err != nil {
		return fmt.Errorf("version check failed: %w", err)
	}

	if !isV11 {
		// Nothing to do, still on v10
		return nil
	}

	if utils.Logger != nil {
		utils.Logger.Info("Detected v11 version: %s", version)
	}
	m.state.Version = version
	m.state.RetryCount = 0

	if err := m.state.SetState(StateDetected); err != nil {
		return fmt.Errorf("failed to update state: %w", err)
	}

	return m.executeMigration()
}

func (m *Migrator) checkVersion() (string, bool, error) {
	versionPath := filepath.Join(config.AgentPath(), "version.json")

	data, err := os.ReadFile(versionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read version.json: %w", err)
	}

	var info VersionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return "", false, fmt.Errorf("failed to parse version.json: %w", err)
	}

	// Check if version starts with "11."
	isV11 := strings.HasPrefix(info.Version, "11.")
	return info.Version, isV11, nil
}

func (m *Migrator) executeMigration() error {
	var err error

	// Read config if not already loaded
	if m.cfg == nil {
		m.cfg, err = config.ReadConfig()
		if err != nil {
			m.state.SetError(err)
			return fmt.Errorf("failed to read config: %w", err)
		}
	}

	// State machine - resume from current state
	// The switch matches the CURRENT state, each function does its work and sets the NEXT state
	for m.state.State != StateDone && m.state.State != StateIdle && m.state.State != StateFailed {
		switch m.state.State {
		case StateDetected:
			err = m.doStopService() // → StateStopping
		case StateStopping:
			err = m.doDownload() // → StateDownloading
		case StateDownloading:
			err = m.doCleanDirectory() // → StateCleaning
		case StateCleaning:
			err = m.doMigrateConfig() // → StateMigratingConfig
		case StateMigratingConfig:
			err = m.doUpdateService() // → StateUpdating
		case StateUpdating:
			err = m.doStartService() // → StateStarting
		case StateStarting:
			err = m.doHealthCheck() // → StateVerifying
		case StateVerifying:
			err = m.doSelfUninstall() // → StateSelfUninstall
		case StateSelfUninstall:
			// Self-uninstall spawned, we're done
			m.state.SetState(StateDone)
			return nil
		}

		if err != nil {
			m.state.SetError(err)
			// Check if we've exhausted retries
			if m.state.RetryCount >= config.MigrationMaxRetries {
				if utils.Logger != nil {
					utils.Logger.ErrorF("Migration failed permanently after %d attempts: %v", m.state.RetryCount, err)
				}
				m.state.SetState(StateFailed)
			}
			return err
		}
	}

	return nil
}

func (m *Migrator) doStopService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Stopping agent service")
	}

	if err := service.StopAgentService(); err != nil {
		return fmt.Errorf("failed to stop service: %w", err)
	}

	return m.state.SetState(StateStopping)
}

func (m *Migrator) doDownload() error {
	if utils.Logger != nil {
		utils.Logger.Info("Downloading new agent binary")
	}

	binaryPath := filepath.Join(config.AgentPath(), config.BinaryName())
	tempPath := binaryPath + ".new"

	// Download to temp file first
	if err := utils.Download(m.cfg.DownloadURL(), tempPath, m.cfg.Insecure); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("download failed: %w", err)
	}

	// Store the temp path for later
	m.state.BinaryPath = tempPath
	if err := m.state.Save(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to save state with binary path: %v", err)
		}
	}

	return m.state.SetState(StateDownloading)
}

func (m *Migrator) doCleanDirectory() error {
	if utils.Logger != nil {
		utils.Logger.Info("Cleaning agent directory")
	}

	// Remove the v10 logs collector service before cleaning (it uses the beats folder)
	if err := service.RemoveLogsCollectorService(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to remove logs collector service: %v (continuing)", err)
		}
		// Continue anyway - best effort
	}

	agentPath := config.AgentPath()
	entries, err := os.ReadDir(agentPath)
	if err != nil {
		return fmt.Errorf("failed to read agent directory: %w", err)
	}

	var cleanupErrors []string
	for _, entry := range entries {
		name := entry.Name()

		// Skip preserved files
		if isPreserved(name) {
			if utils.Logger != nil {
				utils.Logger.LogF(100, "Preserving: %s", name)
			}
			continue
		}

		// Skip the temp download file
		if strings.HasSuffix(name, ".new") {
			continue
		}

		fullPath := filepath.Join(agentPath, name)
		if utils.Logger != nil {
			utils.Logger.LogF(100, "Removing: %s", fullPath)
		}

		if entry.IsDir() {
			if err := os.RemoveAll(fullPath); err != nil {
				errMsg := fmt.Sprintf("failed to remove directory %s: %v", fullPath, err)
				cleanupErrors = append(cleanupErrors, errMsg)
				if utils.Logger != nil {
					utils.Logger.ErrorF("%s", errMsg)
				}
			}
		} else {
			if err := os.Remove(fullPath); err != nil {
				errMsg := fmt.Sprintf("failed to remove file %s: %v", fullPath, err)
				cleanupErrors = append(cleanupErrors, errMsg)
				if utils.Logger != nil {
					utils.Logger.ErrorF("%s", errMsg)
				}
			}
		}
	}

	// Log accumulated errors but don't fail - cleanup is best-effort
	if len(cleanupErrors) > 0 && utils.Logger != nil {
		utils.Logger.ErrorF("Cleanup completed with %d errors (non-fatal)", len(cleanupErrors))
	}

	return m.state.SetState(StateCleaning)
}

func (m *Migrator) doMigrateConfig() error {
	if utils.Logger != nil {
		utils.Logger.Info("Migrating log-collector-config.json from v10 to v11 format")
	}

	migrated, err := MigrateCollectorConfig()
	if err != nil {
		// Config migration failure is not fatal - agent can still work
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to migrate collector config: %v (continuing)", err)
		}
	} else if migrated && utils.Logger != nil {
		utils.Logger.Info("Collector config migration completed successfully")
	}

	return m.state.SetState(StateMigratingConfig)
}

func isPreserved(name string) bool {
	for _, p := range config.PreservedFiles {
		if name == p {
			return true
		}
	}
	return false
}

func (m *Migrator) doUpdateService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Updating service configuration")
	}

	binaryPath := filepath.Join(config.AgentPath(), config.BinaryName())
	tempPath := m.state.BinaryPath

	// Move temp file to final location
	if tempPath != "" {
		if err := os.Rename(tempPath, binaryPath); err != nil {
			// Try copy + delete if rename fails (cross-device)
			data, readErr := os.ReadFile(tempPath)
			if readErr != nil {
				return fmt.Errorf("failed to read temp binary: %w", readErr)
			}
			if writeErr := os.WriteFile(binaryPath, data, 0755); writeErr != nil {
				return fmt.Errorf("failed to write binary: %w", writeErr)
			}
			os.Remove(tempPath)
		}
	}

	// Set executable permissions on Linux
	if err := setExecutable(binaryPath); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to set executable permission: %v", err)
		}
		// Continue anyway, might still work
	}

	// Update service configuration
	if err := service.UpdateServiceExecPath(binaryPath); err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	return m.state.SetState(StateUpdating)
}

func (m *Migrator) doStartService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Starting agent service")
	}

	if err := service.StartAgentService(); err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}

	return m.state.SetState(StateStarting)
}

func (m *Migrator) doHealthCheck() error {
	if utils.Logger != nil {
		utils.Logger.Info("Performing health check (waiting %v)", config.HealthCheckWait)
	}

	// Wait for service to stabilize
	time.Sleep(config.HealthCheckWait)

	if !service.IsAgentServiceRunning() {
		return fmt.Errorf("agent service is not running after health check")
	}

	if utils.Logger != nil {
		utils.Logger.Info("Health check passed - agent service is running")
	}
	return m.state.SetState(StateVerifying)
}

func (m *Migrator) doSelfUninstall() error {
	if utils.Logger != nil {
		utils.Logger.Info("Migration complete, initiating self-uninstall")
	}

	if err := m.state.SetState(StateSelfUninstall); err != nil {
		return err
	}

	return SelfUninstall()
}
