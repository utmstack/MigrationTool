package upgrade

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/converters"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/exporter"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/searchdata"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/state"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

const (
	// V12 releases are GitHub prereleases, so 'releases/latest' resolves to a
	// V11 tag. Pin the exact V12 the migration targets.
	V12ReleaseTag        = "v12.0.0"
	InstallerURL         = "https://github.com/utmstack/UTMStack/releases/download/" + V12ReleaseTag + "/installer"
	SSHReconnectAttempts = 30
	SSHReconnectDelay    = 10 * time.Second
	PostInstallWaitTime  = 60 * time.Second
)

type Upgrader struct {
	sourceCfg   domain.InstanceConfig
	databases   []domain.DatabaseSource
	backupFile  string
	executor    remote.CommandExecutor
	totalTables int
	isLocal     bool
	withData    bool
	phases      *searchdata.Phases
	osImage     string
	osPassword  string
	// st enables per-step checkpoints: completed steps are skipped on re-run.
	st *state.State
	// portWait overrides the fixed post-publish wait; tests set it to 0.
	portWait time.Duration
	// streamFn is the streaming step; defaults to phases.StreamPhantom. Swapped in tests.
	streamFn func(apiAddr, osPass string) error
	// confirmFn replaces validateAndConfirm in tests.
	confirmFn func() error
}

func (u *Upgrader) publishPort(purpose string, wait time.Duration) {
	if u.portWait > 0 {
		wait = u.portWait
	}
	ui.StartOperation(fmt.Sprintf("Opening database port for %s", purpose))
	done := make(chan error, 1)
	go func() {
		_, openErr := u.executor.ExecuteCommandWithSudo("docker service update --publish-add 5432:5432 utmstack_postgres")
		done <- openErr
	}()
	select {
	case openErr := <-done:
		if openErr != nil {
			ui.Warning(fmt.Sprintf("Failed to open port: %v", openErr))
			return
		}
		ui.Success("Database port opened")
		time.Sleep(wait)
	case <-time.After(30 * time.Second):
		ui.Warning("Timeout opening database port (30s)")
	}
}

// WithState enables checkpointing for the run.
func (u *Upgrader) WithState(st *state.State) *Upgrader {
	u.st = st
	return u
}

func (u *Upgrader) stepDone(name string) bool {
	return u.st != nil && u.st.Done(name)
}

// runStep executes a step unless a previous run already completed it. The
// step is marked only after fn succeeds, so a failed step re-runs on resume.
func (u *Upgrader) runStep(name, desc string, fn func() error) error {
	if u.stepDone(name) {
		ui.Info(fmt.Sprintf("%s already done, skipping", desc))
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	if u.st != nil {
		return u.st.Mark(name)
	}
	return nil
}

func New(sourceCfg domain.InstanceConfig, databases []domain.DatabaseSource, backupFile string, withData bool) *Upgrader {
	totalTables := 0
	for _, db := range databases {
		totalTables += len(db.Tables)
	}

	return &Upgrader{
		sourceCfg:   sourceCfg,
		databases:   databases,
		backupFile:  backupFile,
		totalTables: totalTables,
		isLocal:     remote.IsLocalhost(sourceCfg.Host),
		withData:    withData,
	}
}

func (u *Upgrader) RunUpgrade() error {
	confirm := u.confirmFn
	if confirm == nil {
		confirm = u.validateAndConfirm
	}
	if err := confirm(); err != nil {
		return err
	}

	if u.executor == nil {
		if err := u.connectExecutor(); err != nil {
			ui.Error(fmt.Sprintf("Failed to connect: %v", err))
			return err
		}
		defer u.executor.Close()
	}

	if u.withData {
		var err error
		u.phases, err = searchdata.NewPhases(searchdata.Config{
			SourceHost:    u.sourceCfg.Host,
			DestHost:      u.sourceCfg.Host,
			SourceExec:    u.executor,
			DestExec:      u.executor,
			SourceSSHUser: u.sourceCfg.SSHUser,
			DestSSHUser:   u.sourceCfg.SSHUser,
		})
		if err != nil {
			ui.Error(fmt.Sprintf("Failed to prepare search data migration: %v", err))
			return err
		}
	}

	// The V11 OpenSearch image and admin password are captured before the
	// cleanup destroys the V11 compose. The upgrade boots a transient
	// OpenSearch node over the preserved data volume with these exact values.
	// On resume the captured values come from the state file: the V11 compose
	// is already gone, so re-reading is impossible.
	if u.withData {
		if u.stepDone(state.StepCaptureOS) && u.st.OS != nil {
			u.osImage, u.osPassword = u.st.OS.Image, u.st.OS.Password
			ui.Info("OpenSearch configuration already captured, skipping")
		} else {
			ui.StartOperation("Capturing V11 OpenSearch configuration")
			var err error
			u.osImage, u.osPassword, err = u.captureOS()
			if err != nil {
				ui.Error(fmt.Sprintf("Failed to capture OpenSearch configuration: %v", err))
				return err
			}
			ui.Success(fmt.Sprintf("OpenSearch image: %s", u.osImage))
			if u.st != nil {
				u.st.OS = &state.OSCapture{Image: u.osImage, Password: u.osPassword}
				if err := u.st.Mark(state.StepCaptureOS); err != nil {
					return err
				}
			}
		}
	}

	// Step 1: Open port for export
	u.publishPort("export", 5*time.Second)

	// Step 2: Export data
	if err := u.runStep(state.StepExport, "Data export", u.exportData); err != nil {
		ui.Error(fmt.Sprintf("Export failed: %v", err))
		return err
	}

	// Note: Agent deployment used to live here as Phase 2. It has been moved to
	// the dedicated `prepare-agents` command, which persists progress in SQLite
	// and can be resumed across runs. The `upgrade` command now assumes the
	// operator has already run `prepare-agents` (or explicitly bypassed the
	// check via --skip-agent-check at the cmd layer).

	// Step 3: Cleanup. Destructive and irreversible: once V11 is gone it must
	// never run again, so the checkpoint is mandatory here.
	if err := u.runStep(state.StepCleanup, "V11 cleanup", u.cleanupInstance); err != nil {
		ui.Error(fmt.Sprintf("Cleanup failed: %v", err))
		return err
	}

	// Step 5: Install V12. Re-running an install on a box that is already V12
	// would destroy the upgraded instance, so it is checkpointed like cleanup.
	if err := u.runStep(state.StepInstall, "V12 installation", u.installV12); err != nil {
		ui.Error(fmt.Sprintf("V12 installation failed: %v", err))
		return err
	}

	// Step 6: Wait for services. Only after a fresh install; on resume the
	// services have long been up.
	if !u.stepDone(state.StepInstall) {
		ui.Blank()
		ui.StartOperation("Waiting for services to initialize")
		time.Sleep(PostInstallWaitTime)
		ui.Success("Services initialized")
	}

	// Step 7: Open port for import
	u.publishPort("import", 10*time.Second)

	// Step 8: Get new password
	ui.StartOperation("Reading new PostgreSQL credentials")
	newPassword, err := u.GetPostgresPasswordFromCompose()
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to read compose.yml: %v", err))
		return err
	}
	ui.Success("PostgreSQL credentials retrieved")

	u.sourceCfg.Password = newPassword

	// Step 9: Import data. Upserts are idempotent, but a completed import is
	// skipped on resume to avoid rewriting every row.
	if err := u.runStep(state.StepImport, "Data import", u.importData); err != nil {
		ui.Error(fmt.Sprintf("Import failed: %v", err))
		return err
	}

	// Load the V11 OpenSearch tenant data into the V12 ClickHouse. The
	// cleanup preserved /utmstack/opensearch; a transient OpenSearch node
	// boots over that volume and the data streams straight into ClickHouse.
	// The node is removed only when the stream finishes without errors.
	// Inside the step, the per-index stream manifest resumes at document level.
	if u.withData {
		err := u.runStep(state.StepStream, "Search data stream", u.streamPhantomData)
		if err != nil {
			ui.Error(fmt.Sprintf("Search data stream failed: %v", err))
			ui.Warning("The transient OpenSearch node stays up for a retry; the data volume is untouched.")
			ui.Warning("Re-running the same command resumes from the last completed step.")
			return err
		}
	}

	// Step 10: Close port
	ui.StartOperation("Closing database port for security")
	closeDone := make(chan error, 1)
	go func() {
		_, closeErr := u.executor.ExecuteCommandWithSudo("docker service update --publish-rm 5432:5432 utmstack_postgres")
		closeDone <- closeErr
	}()
	select {
	case closeErr := <-closeDone:
		if closeErr != nil {
			ui.Warning(fmt.Sprintf("Failed to close port: %v", closeErr))
		} else {
			ui.Success("Database port closed")
		}
	case <-time.After(30 * time.Second):
		ui.Warning("Timeout closing database port (30s)")
	}

	ui.FinalSuccess("Upgrade completed successfully")

	// A fully completed run leaves no checkpoint behind.
	if u.st != nil {
		if err := state.Delete(u.st.Path); err != nil {
			ui.Warning(fmt.Sprintf("Could not delete state file %s: %v", u.st.Path, err))
			ui.Warning("Delete it manually; it is not needed for the upgraded instance.")
		}
	}

	ui.NextSteps([]string{
		"Verify services are running: docker service ls",
		"Test the V12 instance",
	})

	return nil
}

func (u *Upgrader) validateAndConfirm() error {
	if !u.withData {
		ui.Blank()
		ui.Warning("Without --with-data the V11 OpenSearch tenant data (logs, alerts, statistics)")
		ui.Warning("will NOT be migrated and will be permanently lost when V11 is removed.")
		ui.Blank()
	} else {
		ui.Blank()
		ui.Warning("--with-data: after the V12 install the tool starts a temporary OpenSearch")
		ui.Warning("container over the preserved /utmstack/opensearch volume and streams the")
		ui.Warning("tenant data into ClickHouse. The container is removed only if the stream")
		ui.Warning("finishes without errors. The volume is never removed by the tool.")
		ui.Blank()
	}
	if !ui.ConfirmDangerous("This will remove UTMStack V11 and install V12. All data will be migrated.") {
		return fmt.Errorf("upgrade cancelled by user")
	}

	ui.Blank()
	ui.Success("Upgrade confirmed")

	return nil
}

func (u *Upgrader) connectExecutor() error {
	if u.isLocal {
		ui.Info("Running in local mode (no SSH needed)")
		u.executor = remote.NewLocalExecutor()

		if u.sourceCfg.SSHUser == "" {
			u.sourceCfg.SSHUser = os.Getenv("SUDO_USER")
			if u.sourceCfg.SSHUser == "" {
				u.sourceCfg.SSHUser = os.Getenv("USER")
			}
			if u.sourceCfg.SSHUser == "" {
				u.sourceCfg.SSHUser = "utmstack"
			}
			ui.Detail("Detected user", u.sourceCfg.SSHUser)
		}

		ui.Success("Local executor ready")
		return nil
	}

	ui.StartOperation("Connecting to instance via SSH")
	sshClient, err := remote.NewSSHClient(remote.SSHConfig{
		Host:       u.sourceCfg.Host,
		Port:       u.sourceCfg.SSHPort,
		User:       u.sourceCfg.SSHUser,
		Password:   u.sourceCfg.SSHPassword,
		PrivateKey: u.sourceCfg.SSHPrivateKey,
	})
	if err != nil {
		return err
	}

	u.executor = sshClient
	ui.Success("SSH connection established")
	return nil
}

func (u *Upgrader) exportData() error {
	ui.SubHeader("Phase 1: Export Data")
	ui.Blank()

	exp := exporter.New(u.sourceCfg, u.totalTables)
	_, err := exp.Export(u.databases, u.sourceCfg.Host, u.backupFile)
	if err != nil {
		return err
	}

	return nil
}

func (u *Upgrader) importData() error {
	ui.SubHeader("Phase 5: Import Data")
	ui.Blank()

	destCfg := u.sourceCfg

	converters.SetIntegrationsConfigDir(destCfg.IntegrationsConfigDir)
	imp := importer.New(destCfg, u.totalTables)
	result, err := imp.Import(u.backupFile)
	if err != nil {
		return err
	}
	result.PrintSummary()

	return nil
}

// NOTE: deployMigrateAgent has been removed from this package. Agent deployment is
// now owned by the `prepare-agents` command (cmd/prepare_agents.go +
// internal/prepareagents/), which persists state in SQLite and supports retries
// and resumable runs.

// streamPhantomData boots the transient OpenSearch node over the preserved
// /utmstack/opensearch volume, streams the tenant data into the V12 ClickHouse,
// and removes the node only when the stream finishes without errors. The data
// volume itself is never touched by this step.
func (u *Upgrader) streamPhantomData() error {
	apiAddr := u.sourceCfg.Host
	if u.isLocal {
		apiAddr = "127.0.0.1"
	}
	phantom := searchdata.NewPhantom(u.executor, u.osImage, u.osPassword)
	if err := phantom.Boot(); err != nil {
		ui.Warning("The V12 install and metadata import already finished; retrying the upgrade data step will not repeat them.")
		return fmt.Errorf("boot transient OpenSearch: %w", err)
	}
	stream := u.streamFn
	if stream == nil {
		stream = func(a, p string) error { return u.phases.StreamPhantom(context.Background(), a, p) }
	}
	if err := stream(apiAddr, u.osPassword); err != nil {
		return err
	}
	if err := phantom.Teardown(); err != nil {
		ui.Warning(fmt.Sprintf("Stream finished, but the transient OpenSearch node could not be removed: %v", err))
		ui.Warning(fmt.Sprintf("Remove it manually with: docker rm -f %s", searchdata.PhantomName))
		return nil
	}
	ui.Success("Transient OpenSearch node removed")
	return nil
}
