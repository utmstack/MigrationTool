package migration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
)

const (
	forwarderRegisterTimeout = 5 * time.Minute
	forwarderRegisterPoll    = 2 * time.Second
	forwarderApplyTimeout    = 2 * time.Minute
)

// tlsCertPreserves maps the V11 agent cert files to the names the V12
// forwarder expects in its own dir.
var tlsCertPreserves = map[string]string{
	"certs/integration.crt":    "certs/integration.crt",
	"certs/integration.key":    "certs/integration.key",
	"certs/integration-ca.crt": "certs/integration_ca.crt",
}

// doForwarder installs the V12 forwarder on Linux endpoints that had enabled
// protocol listeners in V11, then enables those listeners through the panel
// so the V12 UI and the forwarder both reflect them. On non-Linux hosts the
// listeners cannot migrate: a visible skip report is written and the
// migration continues.
func (m *Migrator) doForwarder(ctx context.Context) error {
	listeners, err := ReadEnabledListeners()
	if err != nil {
		// No extracted config means no listeners to preserve. Not a failure.
		listeners = nil
	}

	if !ForwarderNeeded(listeners) {
		if len(listeners) > 0 {
			if werr := writeSkippedReport(listeners); werr != nil && utils.Logger != nil {
				utils.Logger.ErrorF("write forwarder skip report: %v", werr)
			}
		}
		return m.state.SetState(StateSelfUninstall)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Installing forwarder for %d enabled listeners", len(listeners))
	}

	if err := m.forwarderInstall(ctx); err != nil {
		return err
	}
	if err := m.forwarderEnable(ctx, listeners); err != nil {
		return err
	}
	return m.state.SetState(StateSelfUninstall)
}

// forwarderInstall downloads the forwarder binary, restores the TLS certs and
// runs the forwarder's own install, which registers it against the panel and
// starts the service. Idempotent: a resumed run skips the install when the
// service already exists.
func (m *Migrator) forwarderInstall(ctx context.Context) error {
	fpath := config.ForwarderPath()
	if fpath == "" {
		return fmt.Errorf("forwarder path not available on %s", runtime.GOOS)
	}
	if forwarderServiceInstalled() {
		if utils.Logger != nil {
			utils.Logger.Info("Forwarder service already installed, skipping")
		}
		return nil
	}

	binaryPath := filepath.Join(fpath, ForwarderBinaryName())
	if err := os.MkdirAll(fpath, 0755); err != nil {
		return fmt.Errorf("create forwarder dir: %w", err)
	}
	if err := utils.Download(forwarderDownloadURL(m.creds.Server), binaryPath, m.creds.Insecure); err != nil {
		return fmt.Errorf("download forwarder: %w", err)
	}
	if err := setExecutable(binaryPath); err != nil && utils.Logger != nil {
		utils.Logger.ErrorF("chmod forwarder: %v", err)
	}
	restoreTLSCerts(fpath)

	panel, err := NewPanel(m.creds.Server, m.creds.InternalKey, m.creds.Insecure)
	if err != nil {
		return err
	}
	defer panel.Close()

	key, err := panel.GetConnectionKey(ctx)
	if err != nil {
		return fmt.Errorf("get connection key: %w", err)
	}

	skip := "no"
	if m.creds.Insecure {
		skip = "yes"
	}
	return runForwarderInstall(binaryPath, fpath, m.creds.Server, key, skip)
}

// forwarderEnable waits for the forwarder to register itself, then pushes one
// config row per enabled listener so the panel (and the V12 UI) holds the
// listener state. A rejected listener is recorded and does not stop the rest.
func (m *Migrator) forwarderEnable(ctx context.Context, listeners []ListenerPort) error {
	panel, err := NewPanel(m.creds.Server, m.creds.InternalKey, m.creds.Insecure)
	if err != nil {
		return err
	}
	defer panel.Close()

	collectorID, err := waitForForwarder(ctx, panel)
	if err != nil {
		return fmt.Errorf("forwarder not registered: %w", err)
	}

	reported := make([]string, 0, len(listeners))
	for _, l := range listeners {
		if eerr := panel.SetIntegration(ctx, collectorID, l.DataType, listenerConfKeys(l)); eerr != nil {
			// An offline collector still gets its row persisted by the panel;
			// the forwarder applies it on its next resync. Verify that before
			// treating this as a failure.
			if aerr := m.waitForIntegrationApplied(ctx, panel, collectorID, l.DataType); aerr != nil {
				if utils.Logger != nil {
					utils.Logger.ErrorF("Enable %s: %v (continuing)", l.DataType, eerr)
				}
				reported = append(reported, fmt.Sprintf("%s %s %s: %v", l.DataType, l.Proto, l.Port, aerr))
				continue
			}
			if utils.Logger != nil {
				utils.Logger.Info("%s applied after forwarder reconnect", l.DataType)
			}
			continue
		}
		if configured, status, lastError, serr := panel.IntegrationState(ctx, collectorID, l.DataType); serr != nil {
			if utils.Logger != nil {
				utils.Logger.ErrorF("Verify %s: %v (continuing)", l.DataType, serr)
			}
			reported = append(reported, fmt.Sprintf("%s: verify failed: %v", l.DataType, serr))
		} else if !configured || status == "rejected" {
			msg := lastError
			if msg == "" {
				msg = "not configured"
			}
			if utils.Logger != nil {
				utils.Logger.ErrorF("Enable %s: %s (continuing)", l.DataType, msg)
			}
			reported = append(reported, fmt.Sprintf("%s: %s", l.DataType, msg))
		}
	}
	if len(reported) > 0 {
		if werr := writeForwarderReport(reported); werr != nil && utils.Logger != nil {
			utils.Logger.ErrorF("write forwarder report: %v", werr)
		}
	}
	return nil
}

// waitForIntegrationApplied polls the persisted config row until the forwarder
// applies it. Sent/Pending means the row is persisted but not yet acked, so it
// keeps waiting; Acked is success; Failed/absent is an error.
func (m *Migrator) waitForIntegrationApplied(ctx context.Context, panel Panel, collectorID uint32, dataType string) error {
	deadline := time.Now().Add(forwarderApplyTimeout)
	for {
		configured, status, lastError, err := panel.IntegrationState(ctx, collectorID, dataType)
		if err == nil {
			switch {
			case configured && status == "Acked":
				return nil
			case !configured || status == "Failed":
				msg := lastError
				if msg == "" {
					msg = fmt.Sprintf("state=%s", status)
				}
				return fmt.Errorf("%s: %s", dataType, msg)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: not applied within %v (last state %q)", dataType, forwarderApplyTimeout, status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(forwarderRegisterPoll):
		}
	}
}

// waitForForwarder polls ListCollector until the forwarder for this host has
// registered, returning its id.
func waitForForwarder(ctx context.Context, panel Panel) (uint32, error) {
	deadline := time.Now().Add(forwarderRegisterTimeout)
	for {
		id, err := panel.LocalForwarderCollectorID(ctx)
		if err == nil {
			return id, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("timeout: %w", err)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(forwarderRegisterPoll):
		}
	}
}

func forwarderServiceInstalled() bool {
	if _, err := os.Stat("/etc/systemd/system/" + config.ForwarderServiceName + ".service"); err == nil {
		return true
	}
	out, err := exec.Command("systemctl", "is-active", config.ForwarderServiceName).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

// restoreTLSCerts copies the preserved V11 certs into the forwarder dir using
// the V12 names. The forwarder refuses to enable TLS listeners without
// integration.crt + integration.key.
func restoreTLSCerts(fpath string) {
	for src, dst := range tlsCertPreserves {
		from := filepath.Join(config.MigrationPath(), src)
		to := filepath.Join(fpath, dst)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		data, rerr := os.ReadFile(from)
		if rerr != nil {
			if utils.Logger != nil {
				utils.Logger.ErrorF("read cert %s: %v", src, rerr)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
			continue
		}
		perm := os.FileMode(0644)
		if strings.HasSuffix(dst, ".key") {
			perm = 0600
		}
		if werr := os.WriteFile(to, data, perm); werr != nil && utils.Logger != nil {
			utils.Logger.ErrorF("restore cert %s: %v", dst, werr)
		}
	}
}

func runForwarderInstall(binaryPath, dir, server, key, skip string) error {
	cmd := exec.Command(binaryPath, "install", server, key, skip)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if utils.Logger != nil {
		utils.Logger.Info("Forwarder install output: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// writeForwarderReport records listeners whose enablement failed, so the tool
// can surface them in its summary.
func writeForwarderReport(lines []string) error {
	path := config.MigrationPath() + "forwarder-report.txt"
	return os.WriteFile(path, []byte("listener enablement problems:\n"+strings.Join(lines, "\n")+"\n"), 0644)
}
