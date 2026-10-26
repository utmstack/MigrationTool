package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// Migrator handles the v11 to v12 agent migration via a clean local reinstall.
type Migrator struct {
	state *StateFile
	creds *config.Credentials
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

	if err := m.checkAndMigrate(ctx); err != nil {
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
			if err := m.checkAndMigrate(ctx); err != nil {
				if utils.Logger != nil {
					utils.Logger.ErrorF("Migration check failed: %v", err)
				}
			}
		}
	}
}

// shouldMigrate fires when the panel serves v12 and the local agent is v11.
func shouldMigrate(served, local string, localExists bool) bool {
	if served == "" || !localExists {
		return false
	}
	return versionMajor(served) == 12 && versionMajor(local) == 11
}

func versionMajor(v string) int {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[0])
	return n
}

// localVersion reads the agent's version.json
func (m *Migrator) localVersion() (string, bool, error) {
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
	return info.Version, true, nil
}

// servedVersion fetches the panel's version.json over 443.
func (m *Migrator) servedVersion(ctx context.Context) (string, error) {
	var info VersionInfo
	if err := utils.DownloadJSON(config.VersionURL(m.creds), &info, m.creds.Insecure); err != nil {
		return "", err
	}
	return info.Version, nil
}

// agentIDOnPanel reports whether our agent id is already on the destination.
// Gating the destructive step prevents a phantom registration from breaking
// the agents import.
func (m *Migrator) agentIDOnPanel(ctx context.Context, id uint32) (bool, error) {
	panel, err := NewPanel(m.creds.Server, m.creds.InternalKey, m.creds.Insecure)
	if err != nil {
		return false, err
	}
	defer panel.Close()

	ids, err := panel.ListAgentIDs(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range ids {
		if a == id {
			return true, nil
		}
	}
	return false, nil
}

func (m *Migrator) checkAndMigrate(ctx context.Context) error {
	if m.state.State == StateFailed || m.state.State == StateDone {
		return nil
	}

	// Resuming a half-done migration
	if m.state.State != StateIdle {
		if m.state.RetryCount >= config.MigrationMaxRetries {
			if utils.Logger != nil {
				utils.Logger.ErrorF("Migration failed after %d attempts, giving up", m.state.RetryCount)
			}
			m.state.SetState(StateFailed)
			return fmt.Errorf("migration failed permanently after %d attempts", m.state.RetryCount)
		}
		if m.state.RetryCount > 0 && utils.Logger != nil {
			utils.Logger.Info("Resuming migration from state: %s (attempt %d/%d)", m.state.State, m.state.RetryCount+1, config.MigrationMaxRetries)
		}
		return m.executeMigration(ctx)
	}

	// Idle: credentials required before anything
	if m.creds == nil {
		creds, err := config.ReadCredentials()
		if err != nil {
			return fmt.Errorf("credentials not ready: %w (retrying)", err)
		}
		m.creds = creds
	}

	localVer, localOk, err := m.localVersion()
	if err != nil {
		return err
	}
	if !localOk {
		if utils.Logger != nil {
			utils.Logger.Info("No local v11 agent, waiting")
		}
		return nil
	}

	served, err := m.servedVersion(ctx)
	if err != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Served version not reachable yet: %v (waiting)", err)
		}
		return nil
	}

	if !shouldMigrate(served, localVer, true) {
		return nil
	}

	if utils.Logger != nil {
		utils.Logger.Info("Detected v11->v12 (served=%s local=%s)", served, localVer)
	}
	m.state.Version = localVer
	m.state.RetryCount = 0

	agentID, err := config.ReadAgentID()
	if err != nil {
		return fmt.Errorf("read agent id: %w", err)
	}
	m.state.AgentID = agentID
	if err := m.state.SetState(StateDetected); err != nil {
		return err
	}

	return m.executeMigration(ctx)
}

func (m *Migrator) executeMigration(ctx context.Context) error {
	for m.state.State != StateDone && m.state.State != StateFailed {
		switch m.state.State {
		case StateDetected:
			if err := m.doGate(ctx); err != nil {
				return m.handleStepError(err)
			}
		case StateUninstalling:
			if err := m.doUninstall(); err != nil {
				return m.handleStepError(err)
			}
		case StateDownloading:
			if err := m.doDownload(); err != nil {
				return m.handleStepError(err)
			}
		case StateInstalling:
			if err := m.doInstall(ctx); err != nil {
				return m.handleStepError(err)
			}
		case StateVerifying:
			if err := m.doVerify(); err != nil {
				return m.handleStepError(err)
			}
		case StateSelfUninstall:
			if err := m.doSelfUninstall(); err != nil {
				return m.handleStepError(err)
			}
		default:
			return fmt.Errorf("unknown state %s", m.state.State)
		}
	}
	return nil
}

// handleStepError records the error and decides whether to retry or give up.
func (m *Migrator) handleStepError(err error) error {
	m.state.SetError(err)
	if m.state.RetryCount >= config.MigrationMaxRetries {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Migration failed permanently after %d attempts: %v", m.state.RetryCount, err)
		}
		m.state.SetState(StateFailed)
	}
	return err
}

// doGate blocks the destructive path until our agent id is on the panel.
// Waiting is not a failure: return nil so the next poll re-checks.
func (m *Migrator) doGate(ctx context.Context) error {
	onPanel, err := m.agentIDOnPanel(ctx, m.state.AgentID)
	if err != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Gate check failed: %v (retrying)", err)
		}
		return err
	}
	if !onPanel {
		if utils.Logger != nil {
			utils.Logger.Info("Agent %d not on panel yet, waiting for agents import", m.state.AgentID)
		}
		return nil // wait, not a failure
	}
	if utils.Logger != nil {
		utils.Logger.Info("Gate passed: agent %d on panel", m.state.AgentID)
	}
	return m.state.SetState(StateUninstalling)
}

// doUninstall extracts preserved files, stops the agent and updater, removes
// the unit files, and wipes the agent dir.
func (m *Migrator) doUninstall() error {
	if utils.Logger != nil {
		utils.Logger.Info("Uninstalling v11 agent locally")
	}

	if err := extractForForwarder(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Extract forwarder files: %v (continuing)", err)
		}
	}

	if err := service.RemoveAgentService(); err != nil {
		return fmt.Errorf("remove agent service: %w", err)
	}

	if err := os.RemoveAll(config.AgentPath()); err != nil {
		return fmt.Errorf("remove agent dir: %w", err)
	}

	return m.state.SetState(StateDownloading)
}

// doDownload fetches the v12 agent binary into the recreated agent dir.
func (m *Migrator) doDownload() error {
	if utils.Logger != nil {
		utils.Logger.Info("Downloading v12 agent binary")
	}

	binaryPath := filepath.Join(config.AgentPath(), config.BinaryName())
	if err := os.MkdirAll(config.AgentPath(), 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}
	if err := utils.Download(config.BinaryDownloadURL(m.creds), binaryPath, m.creds.Insecure); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if err := setExecutable(binaryPath); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("chmod: %v", err)
		}
	}

	return m.state.SetState(StateInstalling)
}

// doInstall fetches the connection key and runs the v12 install, which
// registers the agent and starts the service.
func (m *Migrator) doInstall(ctx context.Context) error {
	if utils.Logger != nil {
		utils.Logger.Info("Installing v12 agent (fetching connection key)")
	}

	panel, err := NewPanel(m.creds.Server, m.creds.InternalKey, m.creds.Insecure)
	if err != nil {
		return err
	}
	defer panel.Close()

	key, err := panel.GetConnectionKey(ctx)
	if err != nil {
		return fmt.Errorf("get connection key: %w", err)
	}

	binaryPath := filepath.Join(config.AgentPath(), config.BinaryName())
	skip := "no"
	if m.creds.Insecure {
		skip = "yes"
	}

	if err := runAgentInstall(binaryPath, m.creds.Server, key, skip); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	return m.state.SetState(StateVerifying)
}

// doVerify waits for the service and checks the version.
func (m *Migrator) doVerify() error {
	if utils.Logger != nil {
		utils.Logger.Info("Verifying v12 agent (waiting %v)", config.HealthCheckWait)
	}

	time.Sleep(config.HealthCheckWait)

	if !service.IsAgentServiceRunning() {
		return fmt.Errorf("agent service is not running after install")
	}

	localVer, localOk, err := m.localVersion()
	if err != nil {
		return err
	}
	if !localOk || versionMajor(localVer) != 12 {
		return fmt.Errorf("agent is not on v12 after install (local=%s ok=%v)", localVer, localOk)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Verified v12 agent running")
	}
	return m.state.SetState(StateSelfUninstall)
}

// doSelfUninstall removes the bridge service.
func (m *Migrator) doSelfUninstall() error {
	if utils.Logger != nil {
		utils.Logger.Info("Migration complete, self-uninstalling")
	}

	if err := m.state.SetState(StateDone); err != nil {
		return err
	}

	return SelfUninstall()
}
